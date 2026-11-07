package native

import (
	"os"
	"path/filepath"
	"strings"
)

// Credentials on this computer are the user's, not the task's: an agent reads them only when the user says so, in every
// mode, "Full access" included. Read-only tools and commands (read, cat, grep…) are otherwise never asked about, so
// nothing stood between an agent and a token in a file: on 2026-11-06 a worker unsure which computer it was on ran
// `cat ~/.gerdr/remote/*.json` and the relay's Cloudflare DNS token went into its chat and to its model's provider.
// A list of the usual places, matched on paths the tools get and on the words of a command — a guard against an agent
// wandering into them, not against one set on getting there (a script it writes can read anything the user can).

// secretPlaces: under the home directory. Gerdr's own (its access token, the hosts it controls, remote access and the
// relay's keys, panels' and providers' keys) and the common places other tools keep theirs.
var secretPlaces = []string{
	".gerdr/token", ".gerdr/server.json", ".gerdr/secrets.json", ".gerdr/peers.json", ".gerdr/remote",
	".ssh", ".aws", ".azure", ".gnupg", ".kube", ".netrc", ".git-credentials", ".npmrc", ".pypirc",
	".docker/config.json", ".config/gh", ".config/gcloud", ".claude/.credentials.json", ".codex/auth.json",
	"Library/Keychains",
}

// secretNames: such files wherever they are (a panel's team sign-in keeps its token in user/data/team.json).
var secretNames = map[string]bool{"secrets.json": true, "team.json": true, "credentials.json": true, ".env": true}

func secretPath(p string) bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	p = filepath.Clean(p)
	if secretNames[filepath.Base(p)] && strings.Contains(p, string(filepath.Separator)+"user"+string(filepath.Separator)) {
		return true
	}
	if filepath.Base(p) == ".env" || filepath.Base(p) == "secrets.json" {
		return true
	}
	rel, err := filepath.Rel(home, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return false
	}
	rel = filepath.ToSlash(rel)
	for _, s := range secretPlaces {
		if rel == s || strings.HasPrefix(rel, s+"/") {
			return true
		}
	}
	return false
}

// touchesSecret: whether this call reads (or lists) a place credentials are kept in.
func touchesSecret(t *tool, args map[string]any, cwd string) bool {
	switch t.name {
	case "read", "write", "edit":
		return secretPath(absPath(str(args, "path"), cwd))
	case "grep", "glob":
		p := str(args, "path")
		if p == "" {
			return false // the working directory: searching it is the work
		}
		a := absPath(p, cwd)
		home, _ := os.UserHomeDir()
		// ~/.gerdr as a whole holds several of them; searching it is reading them
		return secretPath(a) || (home != "" && a == filepath.Join(home, ".gerdr"))
	case "bash":
		return secretCommand(str(args, "command"), cwd)
	}
	return false
}

// credFiles: file names that are credentials whatever directory a command reaches them from.
var credFiles = map[string]bool{
	".netrc": true, ".git-credentials": true, ".npmrc": true, ".pypirc": true, "id_rsa": true, "id_ed25519": true, "id_ecdsa": true,
	"remote.json": true, "peers.json": true, "secrets.json": true, "server.json": true,
}

// secretCommand: a command naming one of those places: a path among its words (~, $HOME and relative ones resolved;
// a glob judged by the part before its first wildcard), or one of Gerdr's credential files by name.
func secretCommand(cmd, cwd string) bool {
	home, _ := os.UserHomeDir()
	for _, w := range strings.FieldsFunc(cmd, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == ';' || r == '|' || r == '&' || r == '(' || r == ')' || r == '<' || r == '>' || r == '='
	}) {
		w = strings.Trim(w, `"'`)
		if w == "" || strings.HasPrefix(w, "-") && !strings.Contains(w, "/") {
			continue
		}
		for _, h := range []string{"$HOME", "${HOME}"} {
			if home != "" && strings.HasPrefix(w, h) {
				w = home + strings.TrimPrefix(w, h)
			}
		}
		if i := strings.IndexAny(w, "*?["); i >= 0 {
			// ~/.gerdr/*/*.json: the directory it starts from is what it reads
			w = w[:i]
			if strings.HasSuffix(w, "/.gerdr/") || w == "~/.gerdr/" || strings.HasSuffix(w, "/.gerdr") {
				return true
			}
		}
		if !strings.ContainsAny(w, "/.") {
			continue
		}
		// Named alone, after a cd the words don't show (cd ~ && cat .netrc)
		if credFiles[filepath.Base(w)] {
			return true
		}
		if secretPath(absPath(w, cwd)) {
			return true
		}
	}
	return false
}
