package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	_ "github.com/joho/godotenv/autoload"
)

const (
	ListTasks = "list_tasks"
	LogTime   = "log_time"
)

type listTasksInput struct {
	Query string `json:"query"`
}
type logTimeInput struct {
	TaskID  string `json:"task_id"`
	Seconds int    `json:"seconds"`
	Comment string `json:"comment,omitempty"`
}

type Task struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Time  int    `json:"logged_seconds"`
}

var tasks = []Task{
	{ID: "1", Title: "database migration"},
	{ID: "2", Title: "API Development"},
	{ID: "3", Title: "frontend update"},
}

const (
	model     = anthropic.ModelClaudeHaiku4_5
	maxTokens = 4096
	maxTurns  = 8
)

func main() {
	client := anthropic.NewClient()

	prompt := "Log 30 minutes to the task about the database migration. And after that (only step by step), log 1 hour to the same task."
	fmt.Println(labeled("user  ", prompt))

	runAgent(context.TODO(), &client, prompt)

	fmt.Printf("\n---- tasks ----\n")
	for _, task := range tasks {
		fmt.Printf("  %-3s %-22s %5ds\n", task.ID, task.Title, task.Time)
	}
}

func runAgent(ctx context.Context, client *anthropic.Client, prompt string) {
	messages := []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
	}
	tools := buildTools()

	for turn := 1; turn <= maxTurns; turn++ {
		fmt.Printf("\n---- turn %d ----\n", turn)
		fmt.Printf("  >>    %d message(s), model=%s, tools=%d\n", len(messages), model, len(tools))

		message, err := client.Messages.New(ctx, anthropic.MessageNewParams{
			MaxTokens: maxTokens,
			Messages:  messages,
			Tools:     tools,
			Model:     model,
			// Thinking:  anthropic.ThinkingConfigParamUnion{OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{}},
			// OutputConfig: anthropic.OutputConfigParam{
			// 	Effort: anthropic.OutputConfigEffortLow,
			// },
		})
		if err != nil {
			fmt.Printf("  !!    request failed: %v\n", err)
			return
		}
		fmt.Printf("[TOKEN DEBUG] Input tokens: %d, Output tokens: %d\n", message.Usage.InputTokens, message.Usage.OutputTokens)
		fmt.Printf("  <<    stop=%s, tokens in=%d out=%d\n\n",
			message.StopReason, message.Usage.InputTokens, message.Usage.OutputTokens)
		messages = append(messages, message.ToParam())

		toolResults := []anthropic.ContentBlockParamUnion{}
		for _, block := range message.Content {
			switch blockTyped := block.AsAny().(type) {
			case anthropic.ToolUseBlock:
				fmt.Printf("  tool  %s  #%s\n", blockTyped.Name, blockTyped.ID)
				fmt.Printf("        in   %s\n", blockTyped.Input)

				switch blockTyped.Name {
				case ListTasks:
					var input listTasksInput
					if err := json.Unmarshal(blockTyped.Input, &input); err != nil {
						toolResults = append(toolResults, toolError(blockTyped.ID, fmt.Errorf(
							`Can't unmarshal json in input for block ID %s. Call again with a JSON object like {"query": "migration"}. Call again with a JSON object like {"query": "migration"}. Error: %w.`, blockTyped.ID, err)))
						continue
					}

					task, err := getTaskByTitle(input.Query)
					if err != nil {
						toolResults = append(toolResults, toolError(blockTyped.ID, fmt.Errorf(
							"Can't find task for for block ID %s by query '%s'. Error: %w", blockTyped.ID, input.Query, err)))
						continue
					}

					toolResults = append(toolResults, toolResultJSON(blockTyped.ID, task))
				case LogTime:
					var input logTimeInput
					if err := json.Unmarshal(blockTyped.Input, &input); err != nil {
						toolResults = append(toolResults, toolError(blockTyped.ID, fmt.Errorf(
							`Can't unmarshal json in input for block ID %s. Call again with {"task_id": "<id>", "seconds": <integer>}. Error: %w.`, blockTyped.ID, err)))
						continue
					}

					if err := logTime(input.TaskID, input.Seconds); err != nil {
						toolResults = append(toolResults, toolError(blockTyped.ID, fmt.Errorf(
							"Can't log time for for block ID %s and task ID %s. Try again with valid task ID. Error: %w", blockTyped.ID, input.TaskID, err)))
						continue
					}

					toolResults = append(toolResults, toolResultJSON(blockTyped.ID, map[string]string{"status": "success"}))
				default:
					toolResults = append(toolResults, toolError(blockTyped.ID, fmt.Errorf(
						"Unknown tool %q. Use only the available tools: %s, %s",
						blockTyped.Name, ListTasks, LogTime)))
				}
			default:
				fmt.Printf("Block %T type=%s\n raw %s\n", blockTyped, block.Type, block.RawJSON())
			}
		}

		switch message.StopReason {
		case anthropic.StopReasonToolUse:
			if len(toolResults) == 0 {
				fmt.Printf("  !!    stop=tool_use, но ни один результат не собран\n")
				return
			}
			fmt.Printf("\n  >>    returning %d tool result(s)\n", len(toolResults))
			messages = append(messages, anthropic.NewUserMessage(toolResults...))

		case anthropic.StopReasonEndTurn:
			fmt.Printf("\n---- done in %d turn(s) ----\n", turn)
			return

		case anthropic.StopReasonPauseTurn:
			fmt.Printf("\n  ..    paused, resending as is\n")

		case anthropic.StopReasonMaxTokens:
			fmt.Printf("  !!    ответ обрезан, упёрлись в MaxTokens=%d\n", maxTokens)
			return

		case anthropic.StopReasonRefusal:
			fmt.Printf("  !!    отказ: category=%q %s\n",
				message.StopDetails.Category, message.StopDetails.Explanation)
			return

		default:
			fmt.Printf("  !!    неожиданный stop_reason=%q\n", message.StopReason)
			return
		}
	}

	fmt.Printf("\n---- остановлено: исчерпан бюджет в %d ход(ов) ----\n", maxTurns)
}

func buildTools() []anthropic.ToolUnionParam {
	listTasksTool := anthropic.ToolParam{
		Name:        ListTasks,
		Description: anthropic.String("Returns a list of tasks. Use it to find the task ID by its name or description."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Optional substring to filter tasks by title, e.g. \"database migration\".",
				},
			},
			ExtraFields: map[string]any{
				"additionalProperties": false,
			},
		},
	}

	logTimeTool := anthropic.ToolParam{
		Name:        LogTime,
		Description: anthropic.String("Logs spent time for a specific task."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "ID of the task",
				},
				"seconds": map[string]any{
					"type":        "integer",
					"description": "How many seconds to log",
					"minimum":     1,
				},
				"comment": map[string]any{
					"type":        "string",
					"description": "Optional comment for the time entry",
				},
			},
			Required: []string{"task_id", "seconds"},
			ExtraFields: map[string]any{
				"additionalProperties": false,
			},
		},
	}

	toolParams := []anthropic.ToolParam{listTasksTool, logTimeTool}
	tools := make([]anthropic.ToolUnionParam, len(toolParams))
	for i, toolParam := range toolParams {
		tools[i] = anthropic.ToolUnionParam{OfTool: &toolParam}
	}
	return tools
}

func getTaskByTitle(title string) (Task, error) {
	if title == "" {
		return Task{}, fmt.Errorf("empty title")
	}
	for _, task := range tasks {
		if strings.Contains(strings.ToLower(task.Title), strings.ToLower(title)) {
			return task, nil
		}
	}
	return Task{}, fmt.Errorf("task %q not found", title)
}

func logTime(taskID string, seconds int) error {
	for i := range tasks {
		if tasks[i].ID == taskID {
			tasks[i].Time += seconds
			fmt.Printf("        log  +%ds to %q, total %ds\n", seconds, tasks[i].Title, tasks[i].Time)
			return nil
		}
	}
	return fmt.Errorf("task with ID %q not found", taskID)
}

func toolResultJSON(toolUseID string, v any) anthropic.ContentBlockParamUnion {
	data, err := json.Marshal(v)
	if err != nil {
		return toolError(toolUseID, fmt.Errorf("encode result: %w", err))
	}
	fmt.Printf("        out  %s\n", data)
	return anthropic.NewToolResultBlock(toolUseID, string(data), false)
}

func toolError(toolUseID string, err error) anthropic.ContentBlockParamUnion {
	fmt.Printf("        ERR  %v\n", err)
	return anthropic.NewToolResultBlock(toolUseID, err.Error(), true)
}

func labeled(label, s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	pad := strings.Repeat(" ", len(label))
	for i := range lines {
		if i == 0 {
			lines[i] = label + lines[i]
			continue
		}
		lines[i] = pad + lines[i]
	}
	return strings.Join(lines, "\n")
}
