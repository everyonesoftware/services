package updates

import (
	"fmt"
	"regexp"
	"services/internal/groups"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var publicationDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var publicationCommit = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
var publicationSource = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,37}[A-Za-z0-9])?/[A-Za-z0-9_.-]{1,100}$`)
var publicationTime = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,15})?Z$`)
var publicationRun = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)

// ValidatePublication validates the complete bounded wire contract, without I/O.
// Times must use the UTC Z spelling; references and identifiers are never repaired.
func ValidatePublication(p groups.ImagePublication) error {
	invalid := func(field string) error { return fmt.Errorf("invalid image publication %s", field) }
	if p.SchemaVersion != 1 {
		return invalid("schemaVersion")
	}
	if len(p.Repository) > 255 || !strings.HasPrefix(p.Repository, "ghcr.io/") {
		return invalid("repository")
	}
	ref, err := parseReference(p.Repository)
	if err != nil || ref.canonicalRepository() != p.Repository || !strings.Contains(ref.repository, "/") {
		return invalid("repository")
	}
	if len(p.Tags) == 0 || len(p.Tags) > 64 {
		return invalid("tags")
	}
	seen := make(map[string]bool, len(p.Tags))
	for _, tag := range p.Tags {
		if !tagPattern.MatchString(tag) || seen[tag] {
			return invalid("tags")
		}
		seen[tag] = true
	}
	if !publicationDigest.MatchString(p.Digest) {
		return invalid("digest")
	}
	if !publicationCommit.MatchString(p.Commit) {
		return invalid("commit")
	}
	if !publicationSource.MatchString(p.SourceRepository) || strings.HasSuffix(p.SourceRepository, "/.") || strings.HasSuffix(p.SourceRepository, "/..") {
		return invalid("sourceRepository")
	}
	if p.SourceRepositoryID != "" {
		id, err := strconv.ParseInt(p.SourceRepositoryID, 10, 64)
		if err != nil || id <= 0 || !publicationRun.MatchString(p.SourceRepositoryID) {
			return invalid("sourceRepositoryId")
		}
	}
	if !boundedText(p.Ref, 1024) || !strings.HasPrefix(p.Ref, "refs/") || len(p.Ref) <= 5 || strings.ContainsAny(p.Ref, " ~^:?*[\\") || strings.Contains(p.Ref, "..") || strings.Contains(p.Ref, "@{") || strings.Contains(p.Ref, "//") || strings.HasSuffix(p.Ref, "/") || strings.HasSuffix(p.Ref, ".") {
		return invalid("ref")
	}
	for _, component := range strings.Split(p.Ref, "/") {
		if strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".lock") {
			return invalid("ref")
		}
	}
	if !publicationRun.MatchString(p.RunID) {
		return invalid("runId")
	}
	if p.RunAttempt <= 0 || p.RunAttempt > 2147483647 {
		return invalid("runAttempt")
	}
	if p.RunURL != "https://github.com/"+p.SourceRepository+"/actions/runs/"+p.RunID {
		return invalid("runUrl")
	}
	if len(p.PublishedAt) > 35 || !publicationTime.MatchString(p.PublishedAt) {
		return invalid("publishedAt")
	}
	if _, err := time.Parse(time.RFC3339Nano, p.PublishedAt); err != nil {
		return invalid("publishedAt")
	}
	if len(p.Version) > 256 || (p.Version != "" && !boundedText(p.Version, 256)) {
		return invalid("version")
	}
	return nil
}
func boundedText(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == unicode.ReplacementChar {
			return false
		}
	}
	return true
}
func copyPublication(p groups.ImagePublication) groups.ImagePublication {
	p.Tags = append([]string(nil), p.Tags...)
	return p
}
