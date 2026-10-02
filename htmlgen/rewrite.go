// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package htmlgen

import (
	"fmt"
	"html/template"
	"text/template/parse"
)

// injectText rewrites a parsed template so that every action which prints is
// piped through the text function. A template author writes {{ .name }} and
// gets {{ .name | text }}; html/template then appends its own escaper after
// it, on the first Execute.
//
// Escaping stays html/template's job. What the rewrite adds is xmlgen's value
// rules ahead of it: a map or slice is an error instead of Go syntax, a
// float64 never prints with an exponent, and the same value renders the same
// way in both packages. Converting at the template rather than over the data
// is what lets the rules also cover a range variable and a map key, and
// leaves the data untouched.
//
// It must run before the first Execute: html/template's escaping pass rewrites
// the same trees then, and refuses changes afterwards.
func injectText(t *template.Template) error {
	// One Parse can produce many trees, not one: {{ define }} and {{ block }}
	// install their bodies as associated templates, and the main tree keeps
	// only a TemplateNode pointing at them. Templates() is exactly the set
	// execution can reach, so walking all of it is complete.
	for _, assoc := range t.Templates() {
		if assoc.Tree == nil || assoc.Tree.Root == nil {
			continue
		}
		// Each tree carries its own pointer into appendText: it is what
		// Tree.ErrorContext reads to locate an error.
		if err := walkList(assoc.Tree, assoc.Tree.Root); err != nil {
			return err
		}
	}
	return nil
}

func walkList(t *parse.Tree, list *parse.ListNode) error {
	if list == nil {
		return nil
	}
	for _, n := range list.Nodes {
		if err := walkNode(t, n); err != nil {
			return err
		}
	}
	return nil
}

func walkNode(t *parse.Tree, n parse.Node) error {
	switch node := n.(type) {
	case *parse.TextNode, *parse.CommentNode, *parse.BreakNode, *parse.ContinueNode:
		return nil

	case *parse.ActionNode:
		if err := checkEscapers(t, node, node.Pipe); err != nil {
			return err
		}
		// A declaration ({{ $x := .y }}) prints nothing, so converting it
		// would change the variable rather than the output. The value is
		// converted later, wherever it is printed.
		if len(node.Pipe.Decl) == 0 {
			appendText(t, node.Pipe)
		}
		return nil

	case *parse.ListNode:
		return walkList(t, node)

	// The branch's own Pipe is a condition, not output, so only the bodies get
	// the text conversion. The condition is still checked for escapers: the
	// value it produces can be printed inside the body as dot.
	case *parse.IfNode:
		return walkBranch(t, node, &node.BranchNode)
	case *parse.RangeNode:
		return walkBranch(t, node, &node.BranchNode)
	case *parse.WithNode:
		return walkBranch(t, node, &node.BranchNode)

	case *parse.TemplateNode:
		// {{ template "x" }}, and the TemplateNode {{ block "x" }} leaves
		// behind, render an associated template. The body is a separate tree,
		// reached by the loop in injectText; this node prints nothing itself,
		// but the value it passes in can be printed there.
		return checkEscapers(t, node, node.Pipe)

	default:
		// An unrecognised node is refused rather than skipped: skipping would
		// mean a construct whose output bypasses the value rules.
		return fmt.Errorf("%w: %T", ErrUnsupportedTemplate, n)
	}
}

func walkBranch(t *parse.Tree, n parse.Node, b *parse.BranchNode) error {
	if err := checkEscapers(t, n, b.Pipe); err != nil {
		return err
	}
	if err := walkList(t, b.List); err != nil {
		return err
	}
	return walkList(t, b.ElseList)
}

// appendText adds "| text" to a pipeline, unless it already ends in it.
func appendText(t *parse.Tree, pipe *parse.PipeNode) {
	if endsWithText(pipe) {
		return
	}
	ident := parse.NewIdentifier(funcText).SetTree(t).SetPos(pipe.Position())
	pipe.Cmds = append(pipe.Cmds, &parse.CommandNode{
		NodeType: parse.NodeCommand,
		Pos:      pipe.Position(),
		Args:     []parse.Node{ident},
	})
}

func endsWithText(pipe *parse.PipeNode) bool {
	if len(pipe.Cmds) == 0 {
		return false
	}
	last := pipe.Cmds[len(pipe.Cmds)-1]
	if len(last.Args) == 0 {
		return false
	}
	ident, ok := last.Args[0].(*parse.IdentifierNode)
	return ok && ident.Ident == funcText
}

// redundantEscapers are the template builtins that escape a value themselves.
// html/template already escapes every value for the context it is printed in,
// so applying one as well escapes the value twice: {{ .x | js }} in a script
// puts the escape sequences themselves into the string. html/template also
// refuses html and urlquery anywhere but at the end of a pipeline, which the
// text conversion appended after them would always violate, with a message
// that points at html/template rather than at the cause.
var redundantEscapers = map[string]bool{"html": true, "js": true, "urlquery": true}

// checkEscapers rejects a pipeline that calls one of the redundant escapers,
// including inside a parenthesised argument.
func checkEscapers(t *parse.Tree, n parse.Node, pipe *parse.PipeNode) error {
	if pipe == nil {
		return nil
	}
	for _, cmd := range pipe.Cmds {
		for _, arg := range cmd.Args {
			switch a := arg.(type) {
			case *parse.IdentifierNode:
				if redundantEscapers[a.Ident] {
					location, _ := t.ErrorContext(n)
					return fmt.Errorf("%w: %s: %s is not needed: every value is already escaped for its context, and applying %s as well escapes it twice",
						ErrUnsupportedTemplate, location, a.Ident, a.Ident)
				}
			case *parse.PipeNode:
				if err := checkEscapers(t, n, a); err != nil {
					return err
				}
			case *parse.ChainNode:
				// A field access on a parenthesized pipeline, (js .x).Y,
				// wraps the pipeline in a ChainNode.
				if p, ok := a.Node.(*parse.PipeNode); ok {
					if err := checkEscapers(t, n, p); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}
