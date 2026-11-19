package publishers

// ReconciliationReview is an encrypted write-ahead approval receipt. It permits
// only retrying the exact binding CAS when the final publisher-state save fails.
type ReconciliationReview struct {
	Fingerprint                                                    string
	ExpectedSource                                                 string
	ExpectedRepositoryID, ExpectedInstallationID, ExpectedRevision int64
	EligibleAfter                                                  string
}

// Reconciliation contains only safe repository metadata, never provider credentials.
type Reconciliation struct {
	State          string `json:"state"`
	PreviousName   string `json:"previousName"`
	CurrentName    string `json:"currentName"`
	RepositoryID   int64  `json:"repositoryId"`
	InstallationID int64  `json:"installationId"`
	Message        string `json:"message"`
	CheckedAt      string `json:"checkedAt,omitempty"`
}
