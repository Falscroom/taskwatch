package domain

import (
	"regexp"
	"strings"
)

// Task — таска YouTrack со связанными MR.
type Task struct {
	ID       string // APP-1234
	Summary  string
	State    string
	URL      string
	Comments []Comment
	MRs      []*MR
}

type Comment struct {
	ID     string
	Author string // логин YouTrack
	Text   string
}

var issueKeyRe = regexp.MustCompile(`(?i)\b([a-z][a-z0-9]+-\d+)\b`)

// IssueKey достаёт ключ таски (APP-1234) из имени ветки, а если там нет —
// из заголовка MR. По нему MR привязывается к таске.
func IssueKey(branch, title string) string {
	for _, s := range []string{branch, title} {
		if m := issueKeyRe.FindStringSubmatch(s); m != nil {
			return strings.ToUpper(m[1])
		}
	}
	return ""
}
