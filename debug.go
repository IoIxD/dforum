package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
)

// Function for returning debug information about a function when
// templating fails us; we strip it to the information we really need.
// We also strip folder names out in the (admittedly rare) case that this
// could give an attacker a hint on how to attack us.
func PublicFacingError(msg string, err error) error {
	// stack trace
	stacktrace := string(debug.Stack())

	pc, filename_, line, _ := runtime.Caller(1)

	// manipulate the stacktrace.
	stacktraceParts := strings.Split(stacktrace, "\n")[3:] // the first three lines are guaranteed to be part of this call.
	var relevant bool                                      // whether we've begun encountering lines that are part of this project
	var maxStackDetail int                                 // the point at which we stop encountering those lines.
	// for each part of the stacktrace...
	for i, v := range stacktraceParts {
		// does it many slashes in it?
		if strings.Count(v, string(os.PathSeparator)) >= 2 {
			// how many tabs in it?
			tabcount := strings.Count(v, "	")
			// split it into parts and filter the line to only the last part
			stacktracePartParts := strings.Split(v, string(os.PathSeparator))
			// make sure it retains the amount of tabs
			var newString string
			for i := 0; i < tabcount; i++ {
				newString += "	"
			}
			newString += stacktracePartParts[len(stacktracePartParts)-1]
			stacktraceParts[i] = newString
		}
		if strings.Contains(v, "LaffForum") {
			if relevant == false {
				relevant = true
			} else {
				maxStackDetail = i + 3
				break
			}
		}
	}

	// and reduce the stacktrace to fit in the scope we want.
	stacktrace = strings.Join(stacktraceParts[0:maxStackDetail], "\n")
	stacktrace += "\n(...continues entering system files...)"
	filenameParts := strings.Split(filename_, "/")
	filename := filenameParts[len(filenameParts)-1]

	funcname_ := runtime.FuncForPC(pc).Name()
	funcnames := strings.Split(funcname_, ".")
	funcname := funcnames[len(funcnames)-1]

	return fmt.Errorf("%v at %v:%v in %v(), %v. \n\n%v", msg, filename, line, funcname, err.Error(), stacktrace)
}

// Same as above, but we don't strip the stacktrace. Useful for
// panic recovery where the entire stacktrace is important.
func PublicFacingErrorUnstripped(err error) error {
	// stack trace
	stacktrace := string(debug.Stack())

	// filename, line, and information we'll use later to get the scope.
	pc, filename_, line, _ := runtime.Caller(1)

	// manipulate the stacktrace.
	stacktraceParts := strings.Split(stacktrace, "\n")
	// for each part of the stacktrace...
	for i, v := range stacktraceParts {
		// does it many slashes in it?
		if strings.Count(v, string(os.PathSeparator)) >= 2 {
			// how many tabs in it?
			tabcount := strings.Count(v, "	")
			// split it into parts and filter the line to only the last part
			stacktracePartParts := strings.Split(v, string(os.PathSeparator))
			// make sure it retains the amount of tabs
			var newString string
			for i := 0; i < tabcount; i++ {
				newString += "	"
			}
			newString += stacktracePartParts[len(stacktracePartParts)-1]
			stacktraceParts[i] = newString
		}
	}

	// reduce the filename to the part we care about.
	filenameParts := strings.Split(filename_, "/")
	filename := filenameParts[len(filenameParts)-1]

	// get the function name
	funcname_ := runtime.FuncForPC(pc).Name()
	funcnames := strings.Split(funcname_, ".")
	funcname := funcnames[len(funcnames)-1]

	return fmt.Errorf("At %v:%v in %v():\n%v. \n\n%v", filename, line, funcname, err.Error(), stacktrace)

}
