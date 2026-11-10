// Curated upstream artwork, bundled locally; provenance is in public/icons/sources.json.
const artwork = (slug, title) => ({ slug, title, src: `/icons/${slug}.png` })

const brands = [
  { match: ['jellyfin'], icon: artwork('jellyfin', 'Jellyfin') },
  { match: ['plex', 'pms-docker', 'plex-media-server'], icon: artwork('plex', 'Plex') },
  { match: ['home-assistant', 'homeassistant'], icon: artwork('homeassistant', 'Home Assistant') },
  { match: ['immich', 'immich-server', 'immich-machine-learning'], icon: artwork('immich', 'Immich') },
  { match: ['nextcloud'], icon: artwork('nextcloud', 'Nextcloud') },
  { match: ['traefik'], icon: artwork('traefikproxy', 'Traefik') },
  { match: ['grafana'], icon: artwork('grafana', 'Grafana') },
  { match: ['prometheus'], icon: artwork('prometheus', 'Prometheus') },
  { match: ['portainer'], icon: artwork('portainer', 'Portainer') },
  { match: ['sonarr'], icon: artwork('sonarr', 'Sonarr') },
  { match: ['radarr'], icon: artwork('radarr', 'Radarr') },
  { match: ['adguard', 'adguardhome'], icon: artwork('adguard', 'AdGuard') },
  { match: ['uptime-kuma', 'uptimekuma'], icon: artwork('uptimekuma', 'Uptime Kuma') },
  { match: ['qbittorrent'], icon: artwork('qbittorrent', 'qBittorrent') },
  { match: ['nginx'], icon: artwork('nginx', 'NGINX') },
  { match: ['tailscale'], icon: artwork('tailscale', 'Tailscale') },
]

export function iconForImage(image = '') {
  // Compare only the repository basename: namespaces and tags are not identities.
  const repository = image.toLowerCase().split('@')[0].split('/').pop().split(':')[0]
  return brands.find(({ match }) => match.includes(repository))?.icon
}

export function iconForProject(project) {
  if (project.name === 'Standalone') return undefined
  const icons = [...new Set(project.containers.map(({ image }) => iconForImage(image)).filter(Boolean))]
  const applications = icons.filter(icon => icon.slug !== 'tailscale')
  if (applications.length === 1) return applications[0]
  if (applications.length > 1) return undefined
  // A VPN-only project may use Tailscale, but an unknown app plus VPN may not.
  return project.containers.every(({ image }) => iconForImage(image)?.slug === 'tailscale') ? icons[0] : undefined
}
