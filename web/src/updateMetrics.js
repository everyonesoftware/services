export function countAvailableUpdates(projects) {
  return projects.reduce((total, project) => (
    total + project.containers.filter((container) => container.updateAvailable).length
  ), 0)
}
