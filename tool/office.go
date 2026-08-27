package tool

import (
	"fmt"

	openagent "github.com/yusheng-g/openagent-go"
)

// officeToolText wraps a success string into a ToolResult.
func officeToolText(text string) *openagent.ToolResult {
	return &openagent.ToolResult{Content: text}
}

// officeToolError wraps an error message into a failed ToolResult.
func officeToolError(toolName, text string) *openagent.ToolResult {
	return openagent.ErrorResult(fmt.Errorf("%s: %s", toolName, text), false, "")
}

// NewOfficeTools returns the office tools (Word + Excel + PPT). workDir is
// the workspace root used for relative-path resolution by all read and write
// tools — relative output paths land in the workspace, not ~/Documents, so
// the agent can read back what it just wrote.
func NewOfficeTools(workDir string) []openagent.Tool {
	return []openagent.Tool{
		&wordReadTool{workDir: workDir},
		&wordWriteTool{workDir: workDir},
		&excelReadTool{workDir: workDir},
		&excelWriteTool{workDir: workDir},
		&pptxReadTool{workDir: workDir},
		&pptxWriteTool{workDir: workDir},
		&pptxTemplateAnalyzeTool{},
		&pptxTemplateFillTool{workDir: workDir},
	}
}
