// Package evalfilter allows running a user-supplied script against an object.
//
// We're constructed with a program, and internally we parse that to an
// abstract syntax-tree, then we walk that tree to generate a series of
// bytecodes.
//
// The bytecode is then executed via the VM-package.
package evalfilter

import (
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/skx/evalfilter/v2/ast"
	"github.com/skx/evalfilter/v2/code"
	"github.com/skx/evalfilter/v2/environment"
	"github.com/skx/evalfilter/v2/lexer"
	"github.com/skx/evalfilter/v2/object"
	"github.com/skx/evalfilter/v2/parser"
	"github.com/skx/evalfilter/v2/vm"
)

// Flags which can be optionally passed to Prepare.
const (
	// Don't run the optimizer when generating bytecode.
	NoOptimize byte = iota
)

// Eval is our public-facing structure which stores our state.
type Eval struct {
	// Script holds the script the user submitted in our constructor.
	Script string

	// Environment
	environment *environment.Environment

	// constants compiled
	constants []object.Object

	// bytecode we generate
	instructions code.Instructions

	// the machine we drive
	machine *vm.VM
}

// New creates a new instance of the evaluator.
func New(script string) *Eval {

	//
	// Create our object.
	//
	e := &Eval{
		environment: environment.New(),
		Script:      script,
	}

	//
	// Return it.
	//
	return e
}

// Prepare is the second function the caller must invoke, it compiles
// the user-supplied program to its final-form.
//
// Internally this compilation process walks through the usual steps,
// lexing, parsing, and bytecode-compilation.
func (e *Eval) Prepare(flags ...[]byte) error {

	//
	// Default to optimizing the bytecode.
	//
	optimize := true

	//
	// But let flags change our behaviour.
	//
	for _, arg := range flags {
		for _, val := range arg {
			if val == NoOptimize {
				optimize = false
			}
		}
	}

	//
	// Create a lexer.
	//
	l := lexer.New(e.Script)

	//
	// Create a parser using the lexer.
	//
	p := parser.New(l)

	//
	// Parse the program into an AST.
	//
	program := p.ParseProgram()

	//
	// Were there any errors produced by the parser?
	//
	// If so report that.
	//
	if len(p.Errors()) > 0 {
		return fmt.Errorf("\nErrors parsing script:\n" +
			strings.Join(p.Errors(), "\n"))
	}

	//
	// Compile the program to bytecode
	//
	err := e.compile(program)

	//
	// If there were errors then return them.
	//
	if err != nil {
		return err
	}

	//
	// Attempt to optimize the code, running multiple passes until no
	// more changes are possible.
	//
	// We do this so that each optimizer run only has to try one thing
	// at a time.
	//
	if optimize {
		e.optimize()
	}

	//
	// Now we're done, construct a VM with the bytecode and constants
	// we've created - as well as any function pointers and variables
	// which we were given.
	//
	e.machine = vm.New(e.constants, e.instructions, e.environment)

	//
	// All done; no errors.
	//
	return nil
}

// Dump causes our bytecode to be dumped.
//
// This is used by the `evalfilter` CLI-utility, but it might be useful
// to consumers of our library.
func (e *Eval) Dump() error {

	i := 0
	fmt.Printf("Bytecode:\n")

	for i < len(e.instructions) {

		// opcode
		op := e.instructions[i]

		// opcode length
		opLen := code.Length(code.Opcode(op))

		// opcode as a string
		str := code.String(code.Opcode(op))

		fmt.Printf("  %06d\t%14s", i, str)

		// show arg
		if op < byte(code.OpCodeSingleArg) {

			arg := binary.BigEndian.Uint16(e.instructions[i+1 : i+3])
			fmt.Printf("\t%d", arg)

			//
			// Show the values, as comments, to make the
			// bytecode more human-readable.
			//
			if code.Opcode(op) == code.OpConstant {

				v := e.constants[arg]
				s := strings.ReplaceAll(v.Inspect(), "\n", "\\n")

				fmt.Printf("\t// load constant: \"%s\"", s)
			}
			if code.Opcode(op) == code.OpLookup {
				fmt.Printf("\t// lookup field: %v", e.constants[arg])
			}
			if code.Opcode(op) == code.OpCall {
				fmt.Printf("\t// call function with %d arg(s)", arg)
			}
		}

		fmt.Printf("\n")

		i += opLen
	}

	// Show constants, if any are present.
	if len(e.constants) > 0 {
		fmt.Printf("\n\nConstants:\n")
		for i, n := range e.constants {

			s := strings.ReplaceAll(n.Inspect(), "\n", "\\n")

			fmt.Printf("  %06d Type:%s Value:\"%s\"\n", i, n.Type(), s)
		}
	}

	return nil
}

// Execute executes the program which the user passed in the constructor,
// and returns the object that the script finished with.
//
// This function is very similar to the `Run` method, however the Run
// method only returns a binary/boolean result, and this method returns
// the actual object your script returned with.
//
// Use of this method allows you to receive the `3` that a script
// such as `return 1 + 2;` would return.
func (e *Eval) Execute(obj interface{}) (object.Object, error) {

	//
	// Launch the program in the VM.
	//
	out, err := e.machine.Run(obj)

	//
	// Error executing?  Report that.
	//
	if err != nil {
		return &object.Null{}, err
	}

	//
	// Return the resulting object.
	//
	return out, nil
}

// Run executes the program which the user passed in the constructor.
//
// The return value, assuming no error, is a binary/boolean result which
// suits the use of this package as a filter.
//
// If you wish to return the actual value the script returned then you can
// use the `Execute` method instead.  That doesn't attempt to determine whether
// the result of the script was "true" or not.
func (e *Eval) Run(obj interface{}) (bool, error) {

	//
	// Execute the script, getting the resulting error
	// and return object.
	//
	out, err := e.Execute(obj)

	//
	// Error? Then return that.
	//
	if err != nil {
		return false, err
	}

	//
	// Otherwise case the resulting object into
	// a boolean and pass that back to the caller.
	//
	return out.True(), nil
}

// AddFunction exposes a golang function from your host application
// to the scripting environment.
//
// Once a function has been added it may be used by the filter script.
func (e *Eval) AddFunction(name string, fun interface{}) {
	e.environment.SetFunction(name, fun)
}

// SetVariable adds, or updates a variable which will be available
// to the filter script.
func (e *Eval) SetVariable(name string, value object.Object) {
	e.environment.Set(name, value)
}

// GetVariable retrieves the contents of a variable which has been
// set within a user-script.
//
// If the variable hasn't been set then the null-value will be returned.
func (e *Eval) GetVariable(name string) object.Object {
	value, ok := e.environment.Get(name)
	if ok {
		return value
	}
	return &object.Null{}
}

// compile is core-code for converting the AST into a series of bytecodes.
func (e *Eval) compile(node ast.Node) error {

	switch node := node.(type) {

	case *ast.Program:
		for _, s := range node.Statements {
			err := e.compile(s)
			if err != nil {
				return err
			}
		}

	case *ast.BlockStatement:
		for _, s := range node.Statements {
			err := e.compile(s)
			if err != nil {
				return err
			}
		}

	case *ast.BooleanLiteral:
		if node.Value {
			e.emit(code.OpTrue)
		} else {
			e.emit(code.OpFalse)
		}

	case *ast.FloatLiteral:
		str := &object.Float{Value: node.Value}
		e.emit(code.OpConstant, e.addConstant(str))

	case *ast.IntegerLiteral:

		// Get the value of the literal
		v := node.Value

		// If this is an integer between 0 & 65535 we
		// can push it naturally.
		if v%1 == 0 && v >= 0 && v <= 65534 {
			e.emit(code.OpPush, int(v))
		} else {

			//
			// Otherwise we emit it as a constant
			// to our pool.
			//
			integer := &object.Integer{Value: node.Value}
			e.emit(code.OpConstant, e.addConstant(integer))
		}

	case *ast.StringLiteral:
		str := &object.String{Value: node.Value}
		e.emit(code.OpConstant, e.addConstant(str))

	case *ast.RegexpLiteral:

		// The regexp body
		val := node.Value

		// The regexp flags
		if node.Flags != "" {

			// Which we pretend were part of the body
			// because that is what Golang expects.
			val = "(?" + node.Flags + ")" + val
		}

		// The value + flags
		reg := &object.String{Value: val}
		e.emit(code.OpConstant, e.addConstant(reg))

	case *ast.ArrayLiteral:
		for _, el := range node.Elements {
			err := e.compile(el)
			if err != nil {
				return err
			}
		}
		e.emit(code.OpArray, len(node.Elements))

	case *ast.ReturnStatement:
		err := e.compile(node.ReturnValue)
		if err != nil {
			return err
		}
		e.emit(code.OpReturn)

	case *ast.ExpressionStatement:
		err := e.compile(node.Expression)
		if err != nil {
			return err
		}

	case *ast.InfixExpression:
		err := e.compile(node.Left)
		if err != nil {
			return err
		}

		err = e.compile(node.Right)
		if err != nil {
			return err
		}

		switch node.Operator {

		// maths
		case "+":
			e.emit(code.OpAdd)
		case "-":
			e.emit(code.OpSub)
		case "*":
			e.emit(code.OpMul)
		case "/":
			e.emit(code.OpDiv)
		case "%":
			e.emit(code.OpMod)
		case "**":
			e.emit(code.OpPower)

			// comparisons
		case "<":
			e.emit(code.OpLess)
		case "<=":
			e.emit(code.OpLessEqual)
		case ">":
			e.emit(code.OpGreater)
		case ">=":
			e.emit(code.OpGreaterEqual)
		case "==":
			e.emit(code.OpEqual)
		case "!=":
			e.emit(code.OpNotEqual)

			// special matches - regexp and array membership
		case "~=":
			e.emit(code.OpMatches)
		case "!~":
			e.emit(code.OpNotMatches)
		case "in":
			e.emit(code.OpArrayIn)

			// logical operators
		case "&&":
			e.emit(code.OpAnd)
		case "||":
			e.emit(code.OpOr)
		default:
			return fmt.Errorf("unknown operator %s", node.Operator)
		}

	case *ast.PrefixExpression:
		err := e.compile(node.Right)
		if err != nil {
			return err
		}

		switch node.Operator {
		case "!":
			e.emit(code.OpBang)
		case "-":
			e.emit(code.OpMinus)
		case "√":
			e.emit(code.OpRoot)
		default:
			return fmt.Errorf("unknown operator %s", node.Operator)
		}

	case *ast.IfExpression:

		// Compile the expression.
		err := e.compile(node.Condition)
		if err != nil {
			return err
		}

		//
		//  Assume the following input:
		//
		//    if ( blah ) {
		//       // A
		//    }
		//    else {
		//       // B
		//    }
		//    // C
		//
		// We've now compiled `blah`, which is the expression
		// above.
		//
		// So now we generate an `OpJumpIfFalse` to handle the case
		// where the if statement is not true. (If the `blah` condition
		// was true we just continue running it ..)
		//
		// Then the jump we're generating here will jump to either
		// B - if there is an else-block - or C if there is not.
		//
		jumpNotTruthyPos := e.emit(code.OpJumpIfFalse, 9999)

		//
		// Compile the code in block A
		//
		err = e.compile(node.Consequence)
		if err != nil {
			return err
		}

		//
		// Here we're calculating the length END of A.
		//
		// Because if the expression was false we want to
		// jump to the START of B.
		//
		afterConsequencePos := len(e.instructions)
		e.changeOperand(jumpNotTruthyPos, afterConsequencePos)

		//
		// If we don't have an `else` block then we're done.
		//
		// If we do then the end of the A-block needs to jump
		// to C - to skip over the else-branch.
		//
		// If there is no else block then we're all good, we only
		// needed to jump over the first block if the condition
		// was not true - and we've already handled that case.
		//
		if node.Alternative != nil {

			//
			// Add a jump to the end of A - which will
			// take us to C.
			//
			// Emit an `OpJump` with a bogus value
			jumpPos := e.emit(code.OpJump, 9999)

			//
			// We're jumping to the wrong place here,
			// so we have to cope with the updated target
			//
			// (We're in the wrong place because we just
			// added a JUMP at the end of A)
			//
			afterConsequencePos = len(e.instructions)
			e.changeOperand(jumpNotTruthyPos, afterConsequencePos)

			//
			// Compile the block
			//
			err := e.compile(node.Alternative)
			if err != nil {
				return err
			}

			//
			// Now we change the offset to be C, which
			// is the end of B.
			//
			afterAlternativePos := len(e.instructions)
			e.changeOperand(jumpPos, afterAlternativePos)
		}

		//
		// Hopefully that is clear.
		//
		// We end up with a simple case where there is no else-clause:
		//
		//   if ..
		//     JUMP IF NOT B:
		//     body
		//     body
		// B:
		//
		// And when there are both we have a pair of jumps:
		//
		//   if ..
		//     JUMP IF NOT B:
		//     body
		//     body
		//     JUMP C:
		//
		//  B: // else clause
		//     body
		//     body
		//     // fall-through
		//  C:
		//

	case *ast.WhileStatement:

		//
		// Record our starting position
		//
		cur := len(e.instructions)

		//
		// Compile the condition.
		//
		err := e.compile(node.Condition)
		if err != nil {
			return err
		}

		//
		//  Assume the following input:
		//
		//    // A
		//    while ( cond ) {
		//       // B
		//       statement(s)
		//       // b2 -> jump to A to retest the condition
		//    }
		//    // C
		//
		// We've now compiled `cond`, which is the expression
		// above.
		//
		// If the condition is false we jump to C, skipping the
		// body.
		//
		// If the condition is true we fall through, and at
		// B2 we jump back to A
		//

		//
		// So now we generate an `OpJumpIfFalse` to handle the case
		// where the condition is not true.
		//
		// This will jump to C, the position after the body.
		//
		jumpNotTruthyPos := e.emit(code.OpJumpIfFalse, 9999)

		//
		// Compile the code in the body
		//
		err = e.compile(node.Body)
		if err != nil {
			return err
		}

		//
		// Append the b2 jump to retry the loop
		//
		e.emit(code.OpJump, cur)

		//
		// Change the jump to skip the block if the condition
		// was false.
		//
		e.changeOperand(jumpNotTruthyPos, len(e.instructions))

	case *ast.AssignStatement:

		// Get the value
		err := e.compile(node.Value)
		if err != nil {
			return err
		}

		// Store the name
		str := &object.String{Value: node.Name.String()}
		e.emit(code.OpConstant, e.addConstant(str))

		// And make it work.
		e.emit(code.OpSet)

	case *ast.Identifier:
		str := &object.String{Value: node.Value}
		e.emit(code.OpLookup, e.addConstant(str))

	case *ast.CallExpression:

		//
		// call to print(1) will have the stack setup as:
		//
		//  1
		//  print
		//  call 1
		//
		// call to print( "steve", "kemp" ) will have:
		//
		//  "steve"
		//  "kemp"
		//  "print"
		//  call 2
		//
		// i.e. We store the arguments on the stack and
		// emit `OpCall NN` where NN is the number of arguments
		// to pop and invoke the function with.
		//
		args := len(node.Arguments)
		for _, a := range node.Arguments {

			err := e.compile(a)
			if err != nil {
				return err
			}
		}

		// call - has the string on the stack
		str := &object.String{Value: node.Function.String()}
		e.emit(code.OpConstant, e.addConstant(str))

		// then a call instruction with the number of args.
		e.emit(code.OpCall, args)

	case *ast.IndexExpression:
		err := e.compile(node.Left)
		if err != nil {
			return err
		}

		err = e.compile(node.Index)
		if err != nil {
			return err
		}

		e.emit(code.OpArrayIndex)

	default:
		return fmt.Errorf("unknown node type %T %v", node, node)
	}
	return nil
}

// addConstant adds a constant to the pool
func (e *Eval) addConstant(obj object.Object) int {

	//
	// Look to see if the constant is present already
	//
	for i, c := range e.constants {

		//
		// If the existing constant has the same
		// type and value - then return the offset.
		//
		if c.Type() == obj.Type() &&
			c.Inspect() == obj.Inspect() {
			return i
		}
	}

	//
	// Otherwise this is a distinct constant and should
	// be added.
	//
	e.constants = append(e.constants, obj)
	return len(e.constants) - 1
}

// emit generates a bytecode operation, and adds it to our program-array.
func (e *Eval) emit(op code.Opcode, operands ...int) int {

	ins := make([]byte, 1)
	ins[0] = byte(op)

	if len(operands) == 1 {

		// Make a buffer for the arg
		b := make([]byte, 2)
		binary.BigEndian.PutUint16(b, uint16(operands[0]))

		// append
		ins = append(ins, b...)
	}

	posNewInstruction := len(e.instructions)
	e.instructions = append(e.instructions, ins...)

	return posNewInstruction
}

// changeOperand is designed to patch the operand of
// and instruction.  It is basically used to rewrite the target
// of our jump instructions in the handling of `if`.
func (e *Eval) changeOperand(opPos int, operand int) {

	// get the opcode
	op := code.Opcode(e.instructions[opPos])

	// make a new buffer for the opcode
	ins := make([]byte, 1)
	ins[0] = byte(op)

	// Make a buffer for the arg
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, uint16(operand))

	// append argument
	ins = append(ins, b...)

	// replace
	e.replaceInstruction(opPos, ins)
}

// replaceInstruction rewrites the instruction at the given
// bytecode position.
func (e *Eval) replaceInstruction(pos int, newInstruction []byte) {
	ins := e.instructions

	for i := 0; i < len(newInstruction); i++ {
		ins[pos+i] = newInstruction[i]
	}
}
