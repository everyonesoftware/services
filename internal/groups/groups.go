package groups

import "sort"

// Status describes the aggregate state of a Compose project.
type Status string

const (
	StatusHealthy   Status = "healthy"
	StatusRunning   Status = "running"
	StatusStarting  Status = "starting"
	StatusUnhealthy Status = "unhealthy"
)

// ImagePublication records untrusted, validated image-publication provenance.
// It describes a registry root manifest/index digest, not deployment authority.
type ImagePublication struct {
	SchemaVersion      int      `json:"schemaVersion"`
	Repository         string   `json:"repository"`
	Tags               []string `json:"tags"`
	Digest             string   `json:"digest"`
	Version            string   `json:"version,omitempty"`
	SourceRepository   string   `json:"sourceRepository"`
	SourceRepositoryID string   `json:"sourceRepositoryId,omitempty"`
	Commit             string   `json:"commit"`
	Ref                string   `json:"ref"`
	RunID              string   `json:"runId"`
	RunAttempt         int      `json:"runAttempt"`
	RunURL             string   `json:"runUrl"`
	PublishedAt        string   `json:"publishedAt"`
}

// Container is the Docker data needed for project health and image-update status.
type Container struct {
	UpdatePublication   *ImagePublication `json:"updatePublication,omitempty"`
	UpdateStatus        string            `json:"updateStatus,omitempty"`
	UpdateError         string            `json:"updateError,omitempty"`
	UpdateCheckedAt     string            `json:"updateCheckedAt,omitempty"`
	RestartCount        int               `json:"restartCount"`
	StartedAt           string            `json:"startedAt,omitempty"`
	ExitCode            int               `json:"exitCode"`
	OOMKilled           bool              `json:"oomKilled"`
	HealthOutput        string            `json:"healthOutput,omitempty"`
	OneShot             bool              `json:"oneShot,omitempty"`
	MonitorStatus       string            `json:"monitorStatus,omitempty"`
	MonitorReason       string            `json:"monitorReason,omitempty"`
	MonitorSnoozedUntil string            `json:"monitorSnoozedUntil,omitempty"`

	ID                      string   `json:"id"`
	Name                    string   `json:"name"`
	Project                 string   `json:"project"`
	Service                 string   `json:"service"`
	Image                   string   `json:"image"`
	ImageCreatedAt          int64    `json:"imageCreatedAt,omitempty"`
	State                   string   `json:"state"`
	Health                  string   `json:"health,omitempty"`
	RepoDigests             []string `json:"-"`
	ComposeWorkingDir       string   `json:"-"`
	ComposeConfigFiles      []string `json:"-"`
	ComposeConfigFilesLabel string   `json:"-"`
	CurrentDigest           string   `json:"currentDigest,omitempty"`
	ProposedDigest          string   `json:"proposedDigest,omitempty"`
	UpdateAvailable         bool     `json:"updateAvailable,omitempty"`
}

// Project groups Compose containers under their Compose project label.
type Project struct {
	Name            string      `json:"name"`
	Status          Status      `json:"status"`
	Containers      []Container `json:"containers"`
	UpdateAvailable bool        `json:"updateAvailable,omitempty"`
}

// Aggregate groups containers and derives one project status. Standalone containers have no Compose update metadata.
func Aggregate(containers []Container) []Project {
	byName := make(map[string][]Container)
	for _, container := range containers {
		if container.UpdatePublication != nil {
			p := *container.UpdatePublication
			p.Tags = append([]string(nil), p.Tags...)
			container.UpdatePublication = &p
		}
		if container.Project == "" {
			container.UpdatePublication = nil
			container.Project = "Standalone"
			container.UpdateAvailable = false
			container.ProposedDigest = ""
			container.ComposeWorkingDir = ""
			container.ComposeConfigFiles = nil
			container.ComposeConfigFilesLabel = ""
		}
		byName[container.Project] = append(byName[container.Project], container)
	}

	projects := make([]Project, 0, len(byName))
	for name, members := range byName {
		updateAvailable := false
		for _, member := range members {
			if member.UpdateAvailable {
				updateAvailable = true
				break
			}
		}
		projects = append(projects, Project{
			Name:            name,
			Status:          projectStatus(members),
			Containers:      members,
			UpdateAvailable: updateAvailable,
		})
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })
	return projects
}

func projectStatus(containers []Container) Status {
	hasHealthCheck := false
	starting := false
	for _, container := range containers {
		switch container.MonitorStatus {
		case "restart-loop", "missing", "unhealthy", "stopped", "oom-killed":
			return StatusUnhealthy
		}
		if container.OneShot && container.State == "exited" && container.ExitCode == 0 && !container.OOMKilled {
			continue
		}
		if container.State != "running" {
			if container.State == "created" {
				starting = true
				continue
			}
			return StatusUnhealthy
		}
		switch container.Health {
		case "healthy":
			hasHealthCheck = true
		case "unhealthy":
			return StatusUnhealthy
		case "starting":
			hasHealthCheck = true
			starting = true
		case "":
			// No configured Docker health check.
		default:
			// Do not report an unknown health state as healthy.
			hasHealthCheck = true
			starting = true
		}
	}
	if starting {
		return StatusStarting
	}
	if hasHealthCheck {
		return StatusHealthy
	}
	return StatusRunning
}
