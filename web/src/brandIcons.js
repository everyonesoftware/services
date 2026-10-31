import {
  siAdguard,
  siGrafana,
  siHomeassistant,
  siImmich,
  siJellyfin,
  siNextcloud,
  siNginx,
  siPlex,
  siPortainer,
  siPrometheus,
  siQbittorrent,
  siRadarr,
  siSonarr,
  siTraefikproxy,
  siUptimekuma,
} from 'simple-icons'

const brands = [
  { match: ['jellyfin'], icon: siJellyfin },
  { match: ['plex'], icon: siPlex },
  { match: ['home-assistant', 'homeassistant'], icon: siHomeassistant },
  { match: ['immich'], icon: siImmich },
  { match: ['nextcloud'], icon: siNextcloud },
  { match: ['traefik'], icon: siTraefikproxy },
  { match: ['grafana'], icon: siGrafana },
  { match: ['prometheus'], icon: siPrometheus },
  { match: ['portainer'], icon: siPortainer },
  { match: ['sonarr'], icon: siSonarr },
  { match: ['radarr'], icon: siRadarr },
  { match: ['adguard'], icon: siAdguard },
  { match: ['uptime-kuma', 'uptimekuma'], icon: siUptimekuma },
  { match: ['qbittorrent'], icon: siQbittorrent },
  { match: ['nginx'], icon: siNginx },
]

export function iconForImage(image = '') {
  const normalized = image.toLowerCase()
  return brands.find(({ match }) => match.some((name) => normalized.includes(name)))?.icon
}

export function iconForProject(project) {
  return project.containers.map(({ image }) => iconForImage(image)).find(Boolean)
}
