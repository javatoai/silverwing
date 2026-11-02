package workspace

import "strings"

func gitMutationCommand(args []string) (string, []string) {
	for i := 0; i < len(args); i++ {
		if args[i] == "-c" {
			i++
			continue
		}
		if strings.HasPrefix(args[i], "-") {
			continue
		}
		return args[i], args[i+1:]
	}
	return "", nil
}

// Checkout/merge normally overwrite ignored untracked files; sync must not.
func materialsGitNoOverwrite(args []string) []string {
	command, tail := gitMutationCommand(args)
	if command != "checkout" && command != "merge" {
		return args
	}
	start := len(args) - len(tail)
	result := append([]string(nil), args[:start]...)
	result = append(result, "--no-overwrite-ignore")
	for _, arg := range tail {
		if arg != "--overwrite-ignore" && arg != "--no-overwrite-ignore" {
			result = append(result, arg)
		}
	}
	return result
}
