// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package xmlgen

import (
	"fmt"
	"text/template"
	"text/template/parse"
)

// injectEscaping rewrites a parsed template so that every action which prints
// is piped through the xml function. A template author writes {{ .name }} and
// gets {{ .name | xml }}, so forgetting to escape is not something a template
// can do.
//
// This is the approach html/template takes for the same problem. Escaping at
// the template rather than over the data is what lets it also cover a range
// variable and a map key, reject a value that has no XML text form, and leave
// the data untouched so resolvers receive the caller's own Go values.
//
// Text outside an action is left alone, which is what makes a literal <null/>
// written in an {{ else }} branch reach the document verbatim.
func injectEscaping(t *template.Template) error {
	// One Parse can produce many trees, not one: {{ define }} and {{ block }}
	// install their bodies as associated templates, and the main tree keeps
	// only a TemplateNode pointing at them.
	//
	// Templates() is exactly the set execution can reach — walkTemplate
	// resolves a name through Lookup, which reads the same map — so walking
	// all of it is complete rather than merely thorough. Walking t.Tree alone
	// left every {{ block }} body unescaped.
	for _, assoc := range t.Templates() {
		if assoc.Tree == nil || assoc.Root == nil {
			continue
		}
		// Each tree carries its own pointer into appendEscape: it is what
		// Tree.ErrorContext reads to locate an error.
		if err := walkList(assoc.Tree, assoc.Root); err != nil {
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
		// A declaration ({{ $x := .y }}) prints nothing, so escaping it would
		// corrupt the variable rather than protect the output. The value is
		// escaped later, wherever it is printed.
		if len(node.Pipe.Decl) == 0 {
			appendEscape(t, node.Pipe)
		}
		return nil

	case *parse.ListNode:
		return walkList(t, node)

	// The branch's own Pipe is a condition, not output, so only the bodies are
	// walked.
	case *parse.IfNode:
		return walkBranch(t, &node.BranchNode)
	case *parse.RangeNode:
		return walkBranch(t, &node.BranchNode)
	case *parse.WithNode:
		return walkBranch(t, &node.BranchNode)

	case *parse.TemplateNode:
		// {{ template "x" }}, and the TemplateNode {{ block "x" }} leaves
		// behind, render an associated template. The body is a separate tree,
		// reached by the loop in injectEscaping; this node prints nothing
		// itself.
		return nil

	default:
		// An unrecognised node is refused rather than skipped: skipping would
		// mean a construct whose output is not escaped.
		return fmt.Errorf("%w: %T", ErrUnsupportedTemplate, n)
	}
}

func walkBranch(t *parse.Tree, b *parse.BranchNode) error {
	if err := walkList(t, b.List); err != nil {
		return err
	}
	return walkList(t, b.ElseList)
}

// appendEscape adds "| xml" to a pipeline, unless it already ends in one of
// the functions that produce final output themselves.
func appendEscape(t *parse.Tree, pipe *parse.PipeNode) {
	if endsWithOutputFunc(pipe) {
		return
	}
	ident := parse.NewIdentifier(funcEscape).SetTree(t).SetPos(pipe.Position())
	pipe.Cmds = append(pipe.Cmds, &parse.CommandNode{
		NodeType: parse.NodeCommand,
		Pos:      pipe.Position(),
		Args:     []parse.Node{ident},
	})
}

func endsWithOutputFunc(pipe *parse.PipeNode) bool {
	if len(pipe.Cmds) == 0 {
		return false
	}
	last := pipe.Cmds[len(pipe.Cmds)-1]
	if len(last.Args) == 0 {
		return false
	}
	ident, ok := last.Args[0].(*parse.IdentifierNode)
	if !ok {
		return false
	}
	switch ident.Ident {
	case funcEscape, funcRaw, funcCDATA:
		return true
	}
	return false
}
