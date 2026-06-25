package transform

import (
	"fmt"
	"regexp"
)

// rewriteBody rewrites a Bitbucket-flavored Markdown blob for GitHub:
//   - bitbucket.org/<repo>/pull-requests/<n>  →  github.com/<gh>/pull/<n>
//   - bitbucket.org/<repo>/issues/<n>         →  github.com/<gh>/issues/<n>
//   - bitbucket.org/<repo>/commits/<hash>     →  github.com/<gh>/commit/<hash>
//   - @bitbucket-nickname                    →  @github-username (when mapped)
//
// Bare "#123" references are NOT rewritten because Bitbucket and GitHub
// number Issues vs PRs differently and we'd risk dangling references.
// Users can re-link manually if needed.
func (t *Transformer) rewriteBody(s string) string {
	if s == "" {
		return ""
	}
	s = t.rewriteRepoURLs(s)
	s = t.rewriteMentions(s)
	return s
}

var bbURLRE = regexp.MustCompile(
	`https://bitbucket\.org/([\w.-]+/[\w.-]+)/(pull-requests?|issues?|commits?|src)/([\w./-]+)`,
)

func (t *Transformer) rewriteRepoURLs(s string) string {
	return bbURLRE.ReplaceAllStringFunc(s, func(match string) string {
		groups := bbURLRE.FindStringSubmatch(match)
		repo := groups[1]
		kind := groups[2]
		tail := groups[3]

		gh, ok := t.cfg.LookupRepo(repo)
		if !ok {
			return match // leave unmapped repos untouched
		}
		switch kind {
		case "pull-requests", "pull-request":
			return fmt.Sprintf("https://github.com/%s/pull/%s", gh, tail)
		case "issues", "issue":
			return fmt.Sprintf("https://github.com/%s/issues/%s", gh, tail)
		case "commits", "commit":
			return fmt.Sprintf("https://github.com/%s/commit/%s", gh, tail)
		case "src":
			return fmt.Sprintf("https://github.com/%s/blob/%s", gh, tail)
		}
		return match
	})
}

// mentionRE matches @nickname in body text. Bitbucket allows letters,
// digits, hyphens, and underscores in usernames. The optional braces form
// {uuid} is also used for legacy mentions.
var mentionRE = regexp.MustCompile(`(^|[^\w])@([\w-]+|\{[\w:-]+\})`)

func (t *Transformer) rewriteMentions(s string) string {
	return mentionRE.ReplaceAllStringFunc(s, func(match string) string {
		groups := mentionRE.FindStringSubmatch(match)
		prefix, ident := groups[1], groups[2]
		if gh, ok := t.cfg.LookupUser(ident); ok {
			return prefix + "@" + gh
		}
		// Strip the @ to avoid silently mentioning some random GitHub account
		// that happens to share the Bitbucket nickname.
		return prefix + ident
	})
}
