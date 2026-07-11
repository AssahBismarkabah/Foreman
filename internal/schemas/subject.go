package schemas

const SubjectPrefix = "foreman"

func Subject(parts ...string) string {
	all := make([]string, 0, len(parts)+1)
	all = append(all, SubjectPrefix)
	all = append(all, parts...)
	result := ""
	for i, p := range all {
		if i > 0 {
			result += "."
		}
		result += p
	}
	return result
}

func SessionSubject(sessionID string) string {
	return Subject("session", sessionID)
}

func SessionEventSubject(sessionID string, evt EventType) string {
	return Subject("session", sessionID, string(evt))
}
