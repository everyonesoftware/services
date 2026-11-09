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
  siTailscale,
  siTraefikproxy,
  siUptimekuma,
} from 'simple-icons'

const brands = [
  { match: ['jellyfin'], icon: siJellyfin },
  { match: ['plex', 'pms-docker', 'plex-media-server'], icon: siPlex },
  { match: ['home-assistant', 'homeassistant'], icon: siHomeassistant },
  { match: ['immich', 'immich-server', 'immich-machine-learning'], icon: siImmich },
  { match: ['nextcloud'], icon: siNextcloud },
  { match: ['traefik'], icon: siTraefikproxy },
  { match: ['grafana'], icon: siGrafana },
  { match: ['prometheus'], icon: siPrometheus },
  { match: ['portainer'], icon: siPortainer },
  { match: ['sonarr'], icon: siSonarr },
  { match: ['radarr'], icon: siRadarr },
  { match: ['adguard', 'adguardhome'], icon: siAdguard },
  { match: ['uptime-kuma', 'uptimekuma'], icon: siUptimekuma },
  { match: ['qbittorrent'], icon: siQbittorrent },
  { match: ['nginx'], icon: siNginx },
  { match: ['tailscale'], icon: siTailscale },
]

export function iconForImage(image = '') {
  // Compare only the repository basename: namespaces and tags are not identities.
  const repository = image.toLowerCase().split('@')[0].split('/').pop().split(':')[0]
  return brands.find(({ match }) => match.includes(repository))?.icon
}

export function iconForProject(project) {
  if (project.name === 'Standalone') return undefined
  const icons = [...new Set(project.containers.map(({ image }) => iconForImage(image)).filter(Boolean))]
  const applications = icons.filter(icon => icon !== siTailscale)
  if (applications.length === 1) return applications[0]
  if (applications.length > 1) return undefined
  // A VPN-only project may use Tailscale, but an unknown app plus VPN may not.
  return project.containers.every(({ image }) => iconForImage(image) === siTailscale) ? icons[0] : undefined
}
