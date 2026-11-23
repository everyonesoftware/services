package githubapp

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Logs publishes the tested Docker archive through a registry-verifying Node
// script, not build-push-action. Do not infer provenance from arbitrary shell
// commands. These exact source pins are paired with verbatim offline fixtures.
const (
	logsPublisherPath  = "scripts/publish-web-image.mjs"
	logsPublishCommand = "node " + logsPublisherPath
	logsWorkflowHash   = "0bc406fe09008564f5d50353f0db861a93c42fe1875318fc5411966c5e9faedd"
	logsPublisherHash  = "eac70245fd6f8e953a854fc78368a877ce47de7c72836890deca8d22dd6179a5"
	logsReleaseAnchor  = "  release:\n"
	logsReleaseOutputs = "    outputs:\n      services_artifact: ${{ steps.services_capture.outputs.artifact }}\n"
	logsStepAnchor     = "      - name: Publish the verified Web image to GitHub Container Registry\n"
	logsStepID         = "        id: logs_registry\n"
	logsCaptureMarker  = "steps.logs_registry.outputs.published_digest"
	logsScriptImport   = "import { appendFileSync, mkdtempSync, rmSync } from 'node:fs';"
	logsOriginalImport = "import { mkdtempSync, rmSync } from 'node:fs';"
	logsScriptAnchor   = "    await promoteLatest(image, token, digest);\n    console.log(`Verified ${host}/${match[1]}:latest at ${digest}, matching ${image}`);\n"
)

const logsCapture = `      - name: Capture original Services publication metadata
        id: services_capture
        if: steps.logs_registry.outcome == 'success' && steps.logs_registry.outputs.published_digest != ''
        continue-on-error: true
        env:
          PUBLISHED_DIGEST: ${{ steps.logs_registry.outputs.published_digest }}
          PUBLISHED_TAGS: |-
            ${{ steps.logs_registry.outputs.published_image }}
            ${{ steps.logs_registry.outputs.published_latest }}
          APP_VERSION: ${{ steps.logs_registry.outputs.published_build }}
          SERVICES_PUBLISH_PAYLOAD_PATH: ${{ runner.temp }}/services-publish/published.json
        run: node scripts/publish-notification.mjs capture
      - name: Retain original Services payload for notification-only retries
        if: steps.services_capture.outcome == 'success'
        continue-on-error: true
        uses: actions/upload-artifact@v7
        with:
          name: ${{ steps.services_capture.outputs.artifact }}
          path: ${{ runner.temp }}/services-publish/published.json
          if-no-files-found: error
          retention-days: 30
          overwrite: false
          include-hidden-files: false
          archive: true
`

// The maintenance path returns before this block. It can promote an older
// image, but must not report that image as a publication of this run's SHA.
const logsScriptOutputs = `    if (env.GITHUB_OUTPUT) {
      try {
        appendFileSync(env.GITHUB_OUTPUT, [
          'published_digest=' + digest,
          'published_image=' + image,
          'published_latest=' + host + '/' + match[1] + ':latest',
          'published_build=' + match[2].slice('0.1.'.length),
          '',
        ].join('\n'));
      } catch {
        console.warn('Could not record verified Web image metadata for notification.');
      }
    }
`

func logsRegistryCount(root *yaml.Node) int {
	jobs := yamlField(root, "jobs")
	if jobs == nil || jobs.Kind != yaml.MappingNode {
		return 0
	}
	count := 0
	for i := 1; i < len(jobs.Content); i += 2 {
		steps := yamlField(jobs.Content[i], "steps")
		if steps == nil || steps.Kind != yaml.SequenceNode {
			continue
		}
		for _, step := range steps.Content {
			if yamlValue(step, "run") == logsPublishCommand {
				count++
			}
		}
	}
	return count
}

func logsSource(text string) (normalized, newline string, ok bool) {
	newline = "\n"
	if strings.Contains(text, "\r\n") {
		if strings.ContainsAny(strings.ReplaceAll(text, "\r\n", ""), "\r\n") {
			return "", "", false
		}
		newline = "\r\n"
	}
	normalized = strings.ReplaceAll(text, "\r\n", "\n")
	return normalized, newline, !strings.Contains(normalized, "\r")
}

func logsSourceMatches(text, hash string) bool {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(text))) == hash
}

func logsNotifyJob() string {
	job := strings.ReplaceAll(fmt.Sprintf(notifyTemplate, "release", "release", "release"), "REPLACE_JOB", "release")
	// A successful release can have intentionally skipped native ancestors.
	// Override implicit success() without allowing cancelled workflows to send.
	guard := "!cancelled() && " + publicationEventGate + " && needs.release.result == 'success' && needs.release.outputs.services_artifact != '' && vars.SERVICES_PUBLISH_NOTIFICATIONS == 'true'"
	return strings.Replace(job, "    if: "+publicationEventGate+" && needs.release.result == 'success' && vars.SERVICES_PUBLISH_NOTIFICATIONS == 'true'", "    if: ${{ "+guard+" }}", 1)
}

func provisionLogsWorkflow(text string) (string, error) {
	source, newline, ok := logsSource(text)
	if !ok || !logsSourceMatches(source, logsWorkflowHash) || !strings.HasSuffix(source, "        run: "+logsPublishCommand+"\n") {
		return "", errors.New("The Logs archive release workflow differs from the reviewed layout; review it manually.")
	}
	source = strings.Replace(source, logsReleaseAnchor, logsReleaseAnchor+logsReleaseOutputs, 1)
	source = strings.Replace(source, logsStepAnchor, logsStepAnchor+logsStepID, 1)
	return strings.ReplaceAll(source+logsCapture+logsNotifyJob(), "\n", newline), nil
}

// Invert only the exact generated instrumentation. This is also the installed
// hook validator: the surviving workflow must still be the reviewed producer.
func originalLogsWorkflow(text string) (string, error) {
	fail := errors.New("The Logs publication hook or its producer differs from the reviewed layout; review it manually.")
	source, newline, ok := logsSource(text)
	suffix := logsCapture + logsNotifyJob()
	if !ok || !strings.HasSuffix(source, suffix) {
		return "", fail
	}
	source = strings.TrimSuffix(source, suffix)
	source = strings.Replace(source, logsReleaseAnchor+logsReleaseOutputs, logsReleaseAnchor, 1)
	source = strings.Replace(source, logsStepAnchor+logsStepID, logsStepAnchor, 1)
	if !logsSourceMatches(source, logsWorkflowHash) {
		return "", fail
	}
	before := strings.ReplaceAll(source, "\n", newline)
	expected, err := provisionLogsWorkflow(before)
	if err != nil || expected != text {
		return "", fail
	}
	return before, nil
}

func provisionLogsPublisher(text string) (string, error) {
	source, newline, ok := logsSource(text)
	if ok && logsSourceMatches(source, logsPublisherHash) && strings.Count(source, logsScriptAnchor) == 1 {
		after := strings.Replace(source, logsOriginalImport, logsScriptImport, 1)
		after = strings.Replace(after, logsScriptAnchor, logsScriptAnchor+logsScriptOutputs, 1)
		return strings.ReplaceAll(after, "\n", newline), nil
	}
	if ok && strings.Count(source, logsScriptAnchor+logsScriptOutputs) == 1 && strings.Count(source, logsScriptImport) == 1 {
		before := strings.Replace(source, logsScriptAnchor+logsScriptOutputs, logsScriptAnchor, 1)
		before = strings.Replace(before, logsScriptImport, logsOriginalImport, 1)
		if logsSourceMatches(before, logsPublisherHash) {
			return text, nil
		}
	}
	return "", errors.New("The Logs registry publisher script is missing or differs from the reviewed source; review it manually.")
}
