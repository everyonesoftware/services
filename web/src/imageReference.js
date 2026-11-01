function digestPreview(digest) {
  return digest.length > 19 ? `${digest.slice(0, 19)}…` : digest
}

export function splitImageReference(value = '') {
  const reference = value.trim()
  if (!reference) return { image: 'Image not reported', tag: '—' }

  if (/^sha256:[a-f0-9]+$/i.test(reference)) {
    return { image: 'Image ID', tag: digestPreview(reference) }
  }

  const digestIndex = reference.lastIndexOf('@')
  const nameAndTag = digestIndex < 0 ? reference : reference.slice(0, digestIndex)
  const digest = digestIndex < 0 ? '' : reference.slice(digestIndex + 1)
  const colonIndex = nameAndTag.lastIndexOf(':')
  const slashIndex = nameAndTag.lastIndexOf('/')
  const hasTag = colonIndex > slashIndex

  return {
    image: hasTag ? nameAndTag.slice(0, colonIndex) : nameAndTag,
    tag: hasTag ? nameAndTag.slice(colonIndex + 1) : digest ? `@${digestPreview(digest)}` : 'latest',
  }
}
