import type { Container } from './types.ts'

const text = (value: unknown): string => typeof value === 'string' ? value : ''
const githubRepository = /^[a-z\d](?:[a-z\d-]{0,37}[a-z\d])?\/[a-z\d_.-]{1,100}$/i
const sha = /^(?:[a-f\d]{40}|[a-f\d]{64})$/i
const rfc3339 = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/i

export function PublicationDetails({ containers }: { containers: Container[] }) {
  const publications = [...new Map(containers.filter(container => container.updateAvailable).map(container => container.updatePublication)
    .filter(publication => publication && typeof publication === 'object' && publication.schemaVersion === 1)
    .map(publication => [JSON.stringify([publication?.version, publication?.sourceRepository, publication?.commit, publication?.runId, publication?.runUrl, publication?.publishedAt, publication?.digest]), publication])).values()]
  return <>{publications.map((publication, index) => {
    if (!publication) return null
    const version = text(publication.version)
    const repository = text(publication.sourceRepository)
    const validRepository = githubRepository.test(repository) && !['.', '..'].includes(repository.split('/')[1])
    const commit = text(publication.commit)
    const commitUrl = validRepository && sha.test(commit) ? `https://github.com/${repository}/commit/${commit}` : ''
    const runId = text(publication.runId)
    // Exact equality rejects other hosts, credentials, ports, query strings and path normalization.
    const expectedRunUrl = validRepository && /^[1-9]\d*$/.test(runId) ? `https://github.com/${repository}/actions/runs/${runId}` : ''
    const runUrl = expectedRunUrl && publication.runUrl === expectedRunUrl ? expectedRunUrl : ''
    const publishedAt = text(publication.publishedAt)
    const validTime = rfc3339.test(publishedAt) && Number.isFinite(Date.parse(publishedAt))
    return <div className="publication-details" key={index}>
      <p className="image-check-note">Publish notification</p>
      <dl className="publication-facts">
        {version && <div><dt>Available version</dt><dd>{version}</dd></div>}
        <div><dt>Published</dt><dd>{validTime ? <time dateTime={publishedAt}>{new Date(publishedAt).toLocaleString()}</time> : 'Not reported'}</dd></div>
        <div><dt>Commit</dt><dd>{commitUrl ? <a href={commitUrl} target="_blank" rel="noopener noreferrer">{commit.slice(0, 7)}</a> : 'Not reported'}</dd></div>
        <div><dt>Workflow</dt><dd>{runUrl ? <a href={runUrl} target="_blank" rel="noopener noreferrer">Workflow run #{runId}</a> : 'Not reported'}</dd></div>
      </dl>
    </div>
  })}</>
}
