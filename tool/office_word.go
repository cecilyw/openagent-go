package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/carmel/gooxml/document"

	openagent "github.com/yusheng-g/openagent-go"
)

// Word (.docx) read/write tools via the pure-Go gooxml library.
//
// word_read extracts paragraph text from an existing .docx. word_write creates
// a new .docx from plain text (one paragraph per line). Neither needs an
// external runtime — both are pure Go.

// ── word_read ──

type wordReadTool struct {
	workDir string
}

type wordReadParams struct {
	Path string `json:"path" jsonschema:"description=Path to the .docx file to read"`
}

func (t *wordReadTool) Definition() openagent.FunctionDefinition {
	return openagent.FunctionDefinition{
		Name: "word_read",
		Description: "Read text content from a Word (.docx) file and return it as plain text. " +
			"Each paragraph becomes a line; runs within a paragraph are concatenated.",
		Parameters: openagent.SchemaOf[wordReadParams](),
	}
}

func (t *wordReadTool) Execute(ctx context.Context, args json.RawMessage) *openagent.ToolResult {
	p, err := openagent.ParseArgs[wordReadParams](args)
	if err != nil {
		return openagent.ErrorResult(fmt.Errorf("word_read: %w", err), false, "")
	}
	abs, err := ValidatePath(t.workDir, p.Path)
	if err != nil {
		return openagent.ErrorResult(fmt.Errorf("word_read: %w", err), false, "")
	}

	text, err := readWordFile(abs)
	if err != nil {
		return officeToolError("word_read", fmt.Sprintf("failed to read Word file: %s", err.Error()))
	}
	if strings.TrimSpace(text) == "" {
		return officeToolError("word_read", "the Word document is empty")
	}
	return officeToolText(text)
}

// ── word_write ──

type wordWriteTool struct {
	workDir string
}

type wordWriteParams struct {
	Path    string `json:"path" jsonschema:"description=Output path for the .docx file. Relative paths resolve to the workspace directory."`
	Content string `json:"content" jsonschema:"description=Text content to write. Newlines become paragraph breaks."`
}

func (t *wordWriteTool) Definition() openagent.FunctionDefinition {
	return openagent.FunctionDefinition{
		Name: "word_write",
		Description: "Write text content to a Word (.docx) file. Creates a new file; overwrites if it already exists. " +
			"Each newline becomes a new paragraph. " +
			"Use this when the user wants a .docx document; use the `write` tool for plain text/code files (.txt, .md, .go, etc.).",
		Parameters: openagent.SchemaOf[wordWriteParams](),
	}
}

func (t *wordWriteTool) Execute(ctx context.Context, args json.RawMessage) *openagent.ToolResult {
	p, err := openagent.ParseArgs[wordWriteParams](args)
	if err != nil {
		return openagent.ErrorResult(fmt.Errorf("word_write: %w", err), false, "")
	}
	if strings.TrimSpace(p.Path) == "" {
		return officeToolError("word_write", "missing required parameter: path")
	}

	outputPath, err := ValidatePath(t.workDir, strings.TrimSpace(p.Path))
	if err != nil {
		return openagent.ErrorResult(fmt.Errorf("word_write: %w", err), false, "")
	}
	if err := writeWordFile(outputPath, p.Content); err != nil {
		return officeToolError("word_write", fmt.Sprintf("failed to write Word file: %s", err.Error()))
	}

	var size int64
	if info, err := os.Stat(outputPath); err == nil {
		size = info.Size()
	}
	return officeToolText(fmt.Sprintf("Successfully wrote Word file: %s (%d bytes)", outputPath, size))
}

// ── gooxml helpers ──

// readWordFile opens a .docx and returns its text, one line per paragraph.
func readWordFile(path string) (string, error) {
	doc, err := document.Open(path)
	if err != nil {
		return "", err
	}
	var parts []string
	for _, para := range doc.Paragraphs() {
		var paraText string
		for _, run := range para.Runs() {
			paraText += run.Text()
		}
		parts = append(parts, paraText)
	}
	return strings.Join(parts, "\n"), nil
}

// writeWordFile creates a .docx from plain text, one paragraph per line.
func writeWordFile(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory %q: %w", dir, err)
	}
	doc := document.New()
	for _, line := range strings.Split(content, "\n") {
		para := doc.AddParagraph()
		run := para.AddRun()
		run.AddText(line)
	}
	return doc.SaveToFile(path)
}
