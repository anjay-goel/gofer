package skill

import _ "embed"

//go:embed SKILL.md
var text string

//go:embed setup/SKILL.md
var setup string

type Skill struct {
	Name string
	Text string
}

var All = []Skill{
	{Name: "gofer", Text: text},
	{Name: "gofer-setup", Text: setup},
}
