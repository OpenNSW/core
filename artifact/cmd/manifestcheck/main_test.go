// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Lanka Software Foundation

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// bundle is a valid one-manifest bundle: a task template, its workflow and render config.
func bundle() map[string]string {
	return map[string]string{
		"b/manifest_v1.json": `{"artifacts":[
			{"id":"apply","kind":"task_template","path":"apply/task.json"},
			{"id":"apply-flow","kind":"workflow","path":"apply/workflow.json"},
			{"id":"apply-render","kind":"generic_template","path":"apply/render.json"},
			{"id":"apply-step","kind":"subtask_template","path":"apply/step.json"}]}`,
		"b/apply/task.json":     `{"id":"apply","workflow_id":"apply-flow","render_config_id":"apply-render"}`,
		"b/apply/workflow.json": `{"id":"apply-flow","nodes":[{"id":"s","type":"TASK","task_template_id":"apply-step"}]}`,
		"b/apply/render.json":   `{}`,
		"b/apply/step.json":     `{"id":"apply-step","task_type":"USER_INPUT"}`,
	}
}

func check(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(context.Background(), args, &out, &errOut)
	return code, out.String() + errOut.String()
}

func TestBundleChecks(t *testing.T) {
	t.Run("a valid bundle passes", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, bundle())
		if code, out := check(t, "-root", root, "b"); code != 0 {
			t.Fatalf("exit %d:\n%s", code, out)
		}
	})

	t.Run("a dangling reference fails", func(t *testing.T) {
		root := t.TempDir()
		files := bundle()
		files["b/apply/task.json"] = `{"id":"apply","workflow_id":"missing-flow","render_config_id":"apply-render"}`
		write(t, root, files)
		code, out := check(t, "-root", root, "b")
		if code != 1 || !strings.Contains(out, `workflow_id "missing-flow" is not listed as workflow`) {
			t.Fatalf("exit %d:\n%s", code, out)
		}
	})

	t.Run("a gap in versions fails", func(t *testing.T) {
		root := t.TempDir()
		files := bundle()
		files["b/manifest_v3.json"] = files["b/manifest_v1.json"]
		write(t, root, files)
		code, out := check(t, "-root", root, "b")
		if code != 1 || !strings.Contains(out, "manifest_v2.json is missing") {
			t.Fatalf("exit %d:\n%s", code, out)
		}
	})

	t.Run("a bundle without versioned manifests fails", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, map[string]string{"b/manifest.json": `{"artifacts":[]}`})
		code, out := check(t, "-root", root, "b")
		if code != 1 || !strings.Contains(out, "no manifest_v1.json") {
			t.Fatalf("exit %d:\n%s", code, out)
		}
	})

	t.Run("no bundles is a usage error", func(t *testing.T) {
		if code, _ := check(t); code != 2 {
			t.Fatalf("exit %d, want 2", code)
		}
	})
}

func TestImmutability(t *testing.T) {
	setup := func(t *testing.T) (base, head string) {
		base, head = t.TempDir(), t.TempDir()
		write(t, base, bundle())
		write(t, head, bundle())
		return base, head
	}

	t.Run("an unchanged bundle plus a new version passes", func(t *testing.T) {
		base, head := setup(t)
		write(t, head, map[string]string{
			"b/apply/render_v2.json": `{"changed":true}`,
			"b/manifest_v2.json": `{"artifacts":[
				{"id":"apply","kind":"task_template","path":"apply/task.json"},
				{"id":"apply-flow","kind":"workflow","path":"apply/workflow.json"},
				{"id":"apply-render","kind":"generic_template","path":"apply/render_v2.json"},
				{"id":"apply-step","kind":"subtask_template","path":"apply/step.json"}]}`,
		})
		if code, out := check(t, "-base", base, "-root", head, "b"); code != 0 {
			t.Fatalf("exit %d:\n%s", code, out)
		}
	})

	t.Run("editing a published artifact fails", func(t *testing.T) {
		base, head := setup(t)
		write(t, head, map[string]string{"b/apply/render.json": `{"changed":true}`})
		code, out := check(t, "-base", base, "-root", head, "b")
		if code != 1 || !strings.Contains(out, "artifact apply/render.json was changed") {
			t.Fatalf("exit %d:\n%s", code, out)
		}
	})

	t.Run("editing a published manifest fails", func(t *testing.T) {
		base, head := setup(t)
		files := bundle()
		write(t, head, map[string]string{"b/manifest_v1.json": files["b/manifest_v1.json"] + "\n"})
		code, out := check(t, "-base", base, "-root", head, "b")
		if code != 1 || !strings.Contains(out, "manifest manifest_v1.json was changed") {
			t.Fatalf("exit %d:\n%s", code, out)
		}
	})

	t.Run("deleting a published artifact fails", func(t *testing.T) {
		base, head := setup(t)
		if err := os.Remove(filepath.Join(head, "b", "apply", "step.json")); err != nil {
			t.Fatal(err)
		}
		code, out := check(t, "-base", base, "-root", head, "b")
		if code != 1 || !strings.Contains(out, "artifact apply/step.json was deleted") {
			t.Fatalf("exit %d:\n%s", code, out)
		}
	})

	t.Run("a bundle new on this branch has nothing to protect", func(t *testing.T) {
		base, head := t.TempDir(), t.TempDir()
		write(t, head, bundle())
		if code, out := check(t, "-base", base, "-root", head, "b"); code != 0 {
			t.Fatalf("exit %d:\n%s", code, out)
		}
	})

	t.Run("a base still on manifest.json has nothing to protect", func(t *testing.T) {
		base, head := t.TempDir(), t.TempDir()
		write(t, base, map[string]string{"b/manifest.json": `{"artifacts":[]}`})
		write(t, head, bundle())
		if code, out := check(t, "-base", base, "-root", head, "b"); code != 0 {
			t.Fatalf("exit %d:\n%s", code, out)
		}
	})
}
