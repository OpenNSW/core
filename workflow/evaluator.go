package engine

import "github.com/expr-lang/expr"

// EvaluateCondition parses and runs a string expression against the global context.
func EvaluateCondition(condition string, context map[string]any) (bool, error) {
	// Empty conditions are default passthroughs
	if condition == "" {
		return true, nil
	}

	program, err := expr.Compile(condition, expr.Env(context), expr.AsBool())
	if err != nil {
		return false, err
	}

	output, err := expr.Run(program, context)
	if err != nil {
		return false, err
	}

	return output.(bool), nil
}
