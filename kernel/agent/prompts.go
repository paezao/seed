package agent

import (
	"embed"
	"strings"
)

//go:embed prompts/*.md
var promptFS embed.FS

// PromptFor returns Prompt(task) for a Seed that goes by name. Once a Seed has
// become something, it is that thing: "Seed" is only what it grew from.
func PromptFor(task, name string) string {
	p := Prompt(task)
	if name == "" || name == "Seed" {
		return p
	}
	return "Your name is " + name + ". You began as a Seed with no purpose and grew into " + name +
		". That is who you are now: speak and present yourself as " + name + " everywhere, never as \"Seed\" " +
		"(Seed is only the technology you are built on).\n\n" + p
}

// Prompt returns the core identity prompt followed by the named task prompt
// (plan, mutate, reflect, chat).
func Prompt(task string) string {
	core, _ := promptFS.ReadFile("prompts/core.md")
	t, err := promptFS.ReadFile("prompts/" + task + ".md")
	if err != nil {
		panic("unknown prompt " + task)
	}
	return strings.TrimSpace(string(core)) + "\n\n" + strings.TrimSpace(string(t))
}
