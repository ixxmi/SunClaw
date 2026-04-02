package permissions

import "strings"

func evaluateShellRules(command string, policy *Policy) (Decision, bool) {
	if policy == nil {
		return Decision{}, false
	}

	command = strings.TrimSpace(command)
	if command == "" {
		return Decision{
			Disposition: DispositionDeny,
			Reason:      "shell command is required",
			MatchedRule: "shell.command.empty",
		}, true
	}

	for _, denied := range policy.shellDenied {
		if strings.Contains(command, denied) {
			return Decision{
				Disposition: DispositionDeny,
				Reason:      "command is denied by shell policy",
				MatchedRule: denied,
			}, true
		}
	}

	if len(policy.shellAllowed) > 0 {
		parts := strings.Fields(command)
		if len(parts) == 0 {
			return Decision{
				Disposition: DispositionDeny,
				Reason:      "shell command is required",
				MatchedRule: "shell.command.empty",
			}, true
		}

		cmdName := parts[0]
		for _, allowed := range policy.shellAllowed {
			if cmdName == allowed {
				return Decision{}, false
			}
		}

		return Decision{
			Disposition: DispositionDeny,
			Reason:      "command is not in shell allowlist",
			MatchedRule: strings.Join(policy.shellAllowed, ","),
		}, true
	}

	return Decision{}, false
}
