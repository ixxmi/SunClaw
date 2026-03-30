package sandbox

import "fmt"

func buildCommand(code, lang string, opts ExecuteOptions) (string, []string, error) {
	switch lang {
	case "bash", "sh", "shell":
		return "sh", []string{"-c", code}, nil
	case "python", "python3":
		return "python3", append([]string{"-c", code}, opts.Args...), nil
	case "node", "javascript", "js":
		return "node", append([]string{"-e", code}, opts.Args...), nil
	default:
		return "", nil, fmt.Errorf("unsupported language: %s", lang)
	}
}
