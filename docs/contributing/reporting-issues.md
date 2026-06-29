# Reporting Issues

We use GitHub Issues to track bugs, feature requests, and improvements. Your detailed reports help us improve the project!

## Before Creating an Issue

1.   **Search existing issues:** Check [open issues](https://github.com/OpenNSW/core/issues) and [closed issues](https://github.com/OpenNSW/core/issues?q=is%3Aissue+is%3Aclosed) to see if your issue has already been reported
2.   **Check documentation:** Review the README and relevant package documentation to see if your question is already answered
3.   **Verify it's a bug:** For behavioral questions, ensure it's actually a bug and not expected behavior

## Reporting a Bug

If you've found a bug, please create a new issue using the bug report template and include:

### Required Information

1.  **Clear and descriptive title**
    - Use a concise summary (e.g., "Workflow interpreter fails on nested EXCLUSIVE_SPLIT evaluation")
    - Avoid vague titles like "Bug" or "Problem"

2.  **Steps to reproduce**
    - Provide step-by-step instructions
    - Include code snippets, config JSON, or commands if applicable
    - Be specific about inputs and actions

3.  **Expected behavior**
    - Describe what should happen

4.  **Actual behavior**
    - Describe what actually happens
    - Include error messages or logs if available

5.  **Environment details**
    - OS and version (e.g., macOS 14.0, Ubuntu 22.04)
    - Go version: `go version`
    - Temporal server version (if applicable)
    - SDK package versions or commit hashes

### Additional Information

-   **Screenshots/Diagrams:** If applicable, include workflow charts, screenshots, or screen recordings
-   **Error logs:** Include relevant log output or stack traces
-   **Minimal reproduction:** If possible, provide a minimal code example or DSL JSON that reproduces the issue
-   **Related issues:** Link to related issues or pull requests

### Example Bug Report

```markdown
**Title:** Workflow interpreter errors when executing nested EXCLUSIVE_SPLIT gateway

**Steps to reproduce:**
1. Define a workflow JSON spec containing a nested EXCLUSIVE_SPLIT gateway.
2. Load and start the workflow via `WorkflowManager`.
3. Signal the task to trigger evaluation.
4. Observe the interpreter error logs.

**Expected behavior:**
Nested branches should evaluate conditions sequentially and transition to the matched next node.

**Actual behavior:**
Fails with evaluation error "gateway transition undefined".

**Environment:**
- OS: macOS 14.0
- Go: 1.26.4
- Package: workflow v0.8.0
- Temporal CLI: v1.1.0
```

## Requesting a Feature

Have an idea for a new feature or improvement? We'd love to hear it! Use the feature request template and include:

### Required Information

1.  **Clear and descriptive title**
    - Summarize the feature (e.g., "Add LankaPay payment gateway integration")

2.  **Detailed description**
    - Explain what the feature should do
    - Describe the user experience, config schema additions, or Go API design

3.  **Problem it solves**
    - What problem does this feature address?
    - What use case does it enable?

4.  **Proposed solution**
    - How should this feature work?
    - Include API design, struct definitions, or configuration examples if applicable

### Additional Information

-   **Alternatives considered:** What other approaches did you consider?
-   **Impact:** Who would benefit from this feature?
-   **Implementation notes:** Any technical considerations or constraints?

### Example Feature Request

```markdown
**Title:** Add LankaPay payment gateway integration

**Description:**
Currently, the payment package supports GovPay webhooks.
For deployments in Sri Lanka, LankaPay is the standard payment processor and should be supported.

**Proposed Solution:**
Implement a pluggable `LankaPay` adapter in the `payment` package that implements the `payment.Gateway` interface.

**Use Case:**
Enables Sri Lankan Single Window instances to process fees directly via the local banking network.
```

## Issue Labels

We use labels to categorize issues:

-   `bug` - Something isn't working
-   `enhancement` - New feature or improvement
-   `documentation` - Documentation improvements
-   `good first issue` - Good for newcomers
-   `help wanted` - Extra attention needed
-   `question` - Further information is requested

## After Submitting

-   **Be responsive:** Respond to questions or requests for clarification
-   **Provide updates:** If you find more information, add it to the issue
-   **Close if resolved:** If you resolved the issue yourself, let us know and close it
-   **Be patient:** We'll review your issue as soon as possible

## Security Issues

**Do not** report security vulnerabilities through public GitHub issues. Instead, please contact the maintainers directly or use GitHub's [private vulnerability reporting](https://github.com/OpenNSW/core/security/advisories/new) feature.

[Open a new issue](https://github.com/OpenNSW/core/issues/new)
