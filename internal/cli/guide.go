package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charlesrobsampson/shh/internal/guide"
)

type guideCmd struct {
	Section string `arg:"" optional:"" help:"A section to go to, by name or the start of it (see --list), such as routines or steps."`
	Search  string `short:"s" help:"Search the guide: opens it at the first match, or with --print writes only the entries that contain the text."`
	List    bool   `help:"List the sections."`
	Print   bool   `help:"Write the guide to the terminal instead of opening it (this is also what happens when output is not a terminal)."`
}

func (c *guideCmd) Run(env *Env) error {
	if c.List {
		fmt.Fprint(env.Stdout, guide.Listing())
		return nil
	}
	sections := guide.Sections
	slug := ""
	if c.Section != "" {
		found, ok := guide.Find(c.Section)
		if !ok {
			return usagef("no single section matches %q; the sections are:\n%s", c.Section, strings.TrimRight(guide.Listing(), "\n"))
		}
		sections, slug = []guide.Section{found}, found.Slug
	}
	query := guide.NewQuery(c.Search)
	if !c.Print && env.IsTerminal && env.Guide != nil {
		return env.Guide(slug, c.Search)
	}
	text := guide.Plain(sections, printWidth(), query)
	if !query.Empty() && strings.TrimSpace(text) == "" {
		return fmt.Errorf("nothing in the guide matches %q", c.Search)
	}
	fmt.Fprint(env.Stdout, text)
	return nil
}

// printWidth is how wide to write the guide: the terminal's width if the shell says, within reason.
func printWidth() int {
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n >= 50 {
		return min(n, 120)
	}
	return 96
}
