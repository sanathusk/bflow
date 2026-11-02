package cli

import (
	"fmt"
	"strings"
	"time"

	"innobytes.tech/bflow/internal/engine"
	"innobytes.tech/bflow/internal/flow"
	"innobytes.tech/bflow/internal/i18n"
	"innobytes.tech/bflow/internal/output"
)

// renderOutcome es el texto para humanos de un comando de flujo.
func renderOutcome(o engine.Outcome, loc *i18n.Localizer) string {
	if loc == nil {
		loc = i18n.DefaultLocalizer()
	}
	var b strings.Builder
	head := o.ID + " · "
	if o.To != "" {
		head += fmt.Sprintf("%s → %s", o.From, o.To)
	} else {
		head += string(o.Phase)
	}
	if o.Round > 0 {
		head += " · " + loc.T("render.round_label", o.Round)
	}
	if o.Branch != "" {
		head += " · " + loc.T("render.branch", o.Branch)
	}
	b.WriteString(head + "\n")
	for _, w := range o.Warnings {
		b.WriteString("  ⚠ " + w + "\n")
	}
	b.WriteString(renderNext(o.Next, loc))
	return strings.TrimRight(b.String(), "\n")
}

func renderView(v engine.View, loc *i18n.Localizer) string {
	if loc == nil {
		loc = i18n.DefaultLocalizer()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s · %s\n", v.ID, v.Title)
	line := "  " + loc.T("render.phase_label", string(v.Phase))
	if v.Lane != "" {
		line += " · " + loc.T("render.lane_label", string(v.Lane))
	}
	if v.Gate != "" {
		line += " · " + loc.T("render.gate_label", v.Gate)
	}
	if v.Round > 0 {
		line += " · " + loc.T("render.round_label", v.Round)
	}
	if v.Since != nil && !v.Since.IsZero() {
		line += " · " + loc.T("render.ago", human(time.Since(*v.Since), loc))
	}
	b.WriteString(line + "\n")
	if v.TrackerState != "" {
		l := "  " + loc.T("render.in_tracker", v.TrackerState)
		if v.TrackerPhase != "" && v.TrackerPhase != flow.Backlog {
			l += loc.T("render.in_tracker_hint", v.TrackerPhase)
		}
		b.WriteString(l + "\n")
	}
	if v.Branch != "" {
		b.WriteString("  " + loc.T("render.branch_label", v.Branch) + "\n")
	}
	if v.PR != nil {
		b.WriteString("  " + loc.T("render.pr_label", v.PR.URL) + "\n")
	}
	if v.Blocked != "" {
		b.WriteString("  " + loc.T("render.blocked_label", v.Blocked) + "\n")
	}
	if v.Pending > 0 {
		b.WriteString("  " + loc.T("render.unsync_warning", v.Pending) + "\n")
	}
	b.WriteString(renderNext(v.Next, loc))
	return strings.TrimRight(b.String(), "\n")
}

func renderNext(n output.Next, loc *i18n.Localizer) string {
	if loc == nil {
		loc = i18n.DefaultLocalizer()
	}
	var b strings.Builder
	switch n.Action {
	case output.ActionAsk:
		b.WriteString(loc.T("render.next_decide", n.Gate))
		if n.Skill != "" {
			b.WriteString(" · " + loc.T("render.skill_label", n.Skill))
		}
		if n.Display != "" {
			b.WriteString("\n\n" + n.Display + "\n")
		}
		b.WriteString("\n  " + n.Question + "\n")
		if n.Display == "" {
			for _, s := range n.Show {
				b.WriteString("  " + loc.T("render.show_before", s) + "\n")
			}
		}
		for i, o := range n.Options {
			fmt.Fprintf(&b, "  %d. %s", i+1, o.Label)
			if o.Command != "" {
				b.WriteString(" → " + o.Command)
			}
			b.WriteString("\n")
		}
	case output.ActionSpawn:
		mode := loc.T("render.spawn_mode")
		if n.Parallel {
			mode = loc.T("render.spawn_parallel_mode")
		}
		b.WriteString(loc.T("render.next_spawn", mode) + "\n")
		for _, a := range n.Agents {
			fmt.Fprintf(&b, "  · %s (%s)\n    %s\n", a.Agent, argLine(a.Args), loc.T("render.reports_label", a.Report))
		}
	case output.ActionWait:
		b.WriteString(loc.T("render.next_wait", n.Reason) + "\n")
	case output.ActionDone:
		if n.Reason != "" {
			b.WriteString(loc.T("render.nothing_pending", n.Reason) + "\n")
		}
	}
	return b.String()
}

func argLine(args map[string]string) string {
	var parts []string
	for _, k := range []string{"phase", "round", "resume", "decision", "note"} {
		if v, ok := args[k]; ok && v != "" && v != "0" {
			if len(v) > 60 {
				v = v[:57] + "…"
			}
			parts = append(parts, k+"="+v)
		}
	}
	return strings.Join(parts, " ")
}

func human(d time.Duration, loc *i18n.Localizer) string {
	if loc == nil {
		loc = i18n.DefaultLocalizer()
	}
	switch {
	case d < time.Minute:
		return loc.T("render.seconds")
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
