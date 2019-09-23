// eval_functions.go contains our in-built functions.

package evalfilter

import (
	"fmt"
	"strings"

	"github.com/skx/evalfilter/object"
)

// fnLen is the implementation of our `len` function.
func fnLen(args []object.Object) object.Object {
	sum := 0
	for _, e := range args {
		sum += len(e.Inspect())
	}
	return &object.Integer{Value: int64(sum)}
}

// fnTrim is the implementation of our `trim` function.
func fnTrim(args []object.Object) object.Object {
	str := ""
	for _, e := range args {
		str += fmt.Sprintf("%v", (e.Inspect()))
	}
	return &object.String{Value: strings.TrimSpace(str)}
}

// fnPrint is the implementation of our `print` function.
func fnPrint(args []object.Object) object.Object {
	for _, e := range args {
		fmt.Printf("%s", e.Inspect())
	}
	return &object.Integer{Value: 0}
}