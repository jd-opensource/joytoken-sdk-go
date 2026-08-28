package toolkit

import "github.com/jd-opensource/joytoken-sdk-go/tooldef"

// evalExpression delegates to tooldef.EvalExpression, the single source of
// truth for arithmetic evaluation. The toolkit Calculator itself is defined in
// tooldef, so this thin wrapper exists only to keep the package's tests (which
// exercise the shared parser) local and to avoid duplicating the parser here.
func evalExpression(input string) (float64, error) {
	return tooldef.EvalExpression(input)
}
