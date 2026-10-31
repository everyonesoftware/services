type UpdateMetricProject = {
  containers: Array<{ updateAvailable?: boolean }>
}

export function countAvailableUpdates(projects: UpdateMetricProject[]): number
