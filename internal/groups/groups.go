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

// Container is the subset of Docker container data needed for project health.
type Container struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Project string `json:"project"`
	Service string `json:"service"`
	Image   string `json:"image"`
	State   string `json:"state"`
	Health  string `json:"health,omitempty"`
}

// Project groups Compose containers under their Compose project label.
type Project struct {
	Name       string      `json:"name"`
	Status     Status      `json:"status"`
	Containers []Container `json:"containers"`
}

// Aggregate groups Compose-managed containers and derives one project status.
// Unlabelled containers are intentionally excluded: Services is Compose-first.
func Aggregate(containers []Container) []Project {
	byName := make(map[string][]Container)
	for _, container := range containers {
		if container.Project == "" {
			continue
		}
		byName[container.Project] = append(byName[container.Project], container)
	}

	projects := make([]Project, 0, len(byName))
	for name, members := range byName {
		projects = append(projects, Project{
			Name:       name,
			Status:     projectStatus(members),
			Containers: members,
		})
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })
	return projects
}

func projectStatus(containers []Container) Status {
	hasHealthCheck := false
	starting := false
	for _, container := range containers {
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

