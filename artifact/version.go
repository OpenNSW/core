package artifact

import (
	"strconv"
	"strings"
)

// defaultVersionLess reports whether version a is older than b. It is numeric-
// aware: "v2" < "v10" (not lexical). Strategy: strip an optional leading "v"/"V";
// if both remaining strings parse as integers, compare numerically; otherwise
// fall back to lexical comparison. Override via WithVersionComparator (e.g. to
// plug in semver).
func defaultVersionLess(a, b string) bool {
	cleanA := strings.TrimPrefix(strings.TrimPrefix(a, "v"), "V")
	cleanB := strings.TrimPrefix(strings.TrimPrefix(b, "v"), "V")

	valA, errA := strconv.Atoi(cleanA)
	valB, errB := strconv.Atoi(cleanB)
	if errA == nil && errB == nil {
		return valA < valB
	}
	return a < b
}
