package hub

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"
)

const (
	narrateEnvelope         = "narrate@0.1"
	narrateArtifactContract = "narrate-artifacts@0.1"
	maxNarrateManifestBytes = 64 << 10
)

// mdtoNarrateArtifacts is the small view model for the audio strip above a narration
// manuscript. The stable manifest is only a pointer; the source hash decides whether that
// pointer is current for the exact manuscript bytes on screen.
type mdtoNarrateArtifacts struct {
	Current      bool
	Stale        bool
	Missing      bool
	AudioHref    string
	GenerateHref string
	Voice        string
	Pace         string
	Provider     string
	Model        string
	Duration     string
	GeneratedAt  string
}

type narrateArtifactManifest struct {
	MarkdownTo string `json:"markdownto"`
	Source     struct {
		Path string `json:"path"`
		Hash string `json:"hash"`
	} `json:"source"`
	Audio struct {
		Path       string `json:"path"`
		MimeType   string `json:"mimeType"`
		DurationMS int64  `json:"durationMs"`
	} `json:"audio"`
	Receipt struct {
		Path string `json:"path"`
	} `json:"receipt"`
	Generation struct {
		Voice      string `json:"voice"`
		Pace       string `json:"pace"`
		Provider   string `json:"provider"`
		Model      string `json:"model"`
		FinishedAt string `json:"finishedAt"`
	} `json:"generation"`
}

// resolveNarrateArtifacts turns a versioned artifact manifest into current/stale/missing UI.
// A malformed or incomplete pointer is treated as missing: Hub never emits a player for an
// arbitrary repository path merely because JSON named it.
func resolveNarrateArtifacts(bare, user, repo, sourcePath, source, generateHref string, canWrite bool) *mdtoNarrateArtifacts {
	manifestPath, basename, versionRoot, ok := narrateManifestLayout(sourcePath)
	if !ok {
		return missingNarrateArtifacts(generateHref, canWrite)
	}
	size, exists := BlobSize("git", bare, defaultRef, manifestPath)
	if !exists || size <= 0 || size > maxNarrateManifestBytes {
		return missingNarrateArtifacts(generateHref, canWrite)
	}
	body, exists := BlobContent("git", bare, defaultRef, manifestPath)
	if !exists {
		return missingNarrateArtifacts(generateHref, canWrite)
	}
	var manifest narrateArtifactManifest
	if json.Unmarshal([]byte(body), &manifest) != nil ||
		manifest.MarkdownTo != narrateArtifactContract ||
		manifest.Source.Path != sourcePath ||
		!sha256Hex(manifest.Source.Hash) ||
		manifest.Audio.MimeType != "audio/mpeg" ||
		manifest.Audio.DurationMS < 0 {
		return missingNarrateArtifacts(generateHref, canWrite)
	}

	audioPath, audioOK := safeRepoPath(manifest.Audio.Path)
	receiptPath, receiptOK := safeRepoPath(manifest.Receipt.Path)
	if !audioOK || !receiptOK || audioPath != manifest.Audio.Path || receiptPath != manifest.Receipt.Path {
		return missingNarrateArtifacts(generateHref, canWrite)
	}
	versionDir := path.Dir(audioPath)
	if audioPath != path.Join(versionDir, basename+".mp3") ||
		receiptPath != path.Join(versionDir, basename+".receipt.json") ||
		!directChild(versionRoot, versionDir) {
		return missingNarrateArtifacts(generateHref, canWrite)
	}
	if audioSize, found := BlobSize("git", bare, defaultRef, audioPath); !found || audioSize <= 0 {
		return missingNarrateArtifacts(generateHref, canWrite)
	}
	if receiptSize, found := BlobSize("git", bare, defaultRef, receiptPath); !found || receiptSize <= 0 {
		return missingNarrateArtifacts(generateHref, canWrite)
	}

	voice, voiceOK := narrateDisplayValue(manifest.Generation.Voice)
	provider, providerOK := narrateDisplayValue(manifest.Generation.Provider)
	model, modelOK := narrateDisplayValue(manifest.Generation.Model)
	pace := strings.ToLower(strings.TrimSpace(manifest.Generation.Pace))
	finished, timeErr := time.Parse(time.RFC3339, manifest.Generation.FinishedAt)
	if !voiceOK || !providerOK || !modelOK || !narratePace(pace) || timeErr != nil {
		return missingNarrateArtifacts(generateHref, canWrite)
	}

	current := manifest.Source.Hash == sourceHash([]byte(source))
	view := &mdtoNarrateArtifacts{
		Current:     current,
		Stale:       !current,
		AudioHref:   "/" + user + "/" + repo + "/raw/" + audioPath,
		Voice:       voice,
		Pace:        pace,
		Provider:    provider,
		Model:       model,
		Duration:    narrateDuration(manifest.Audio.DurationMS),
		GeneratedAt: finished.UTC().Format("Jan 2, 2006"),
	}
	if view.Stale && canWrite {
		view.GenerateHref = generateHref
	}
	return view
}

func missingNarrateArtifacts(generateHref string, canWrite bool) *mdtoNarrateArtifacts {
	if !canWrite {
		return nil
	}
	return &mdtoNarrateArtifacts{Missing: true, GenerateHref: generateHref}
}

func narrateManifestLayout(sourcePath string) (manifestPath, basename, versionRoot string, ok bool) {
	clean, ok := safeRepoPath(sourcePath)
	if !ok || clean != sourcePath || !strings.EqualFold(path.Ext(clean), ".md") {
		return "", "", "", false
	}
	name := path.Base(clean)
	basename = name[:len(name)-len(path.Ext(name))]
	if basename == "" {
		return "", "", "", false
	}
	parent := path.Dir(clean)
	root := "narrate"
	if parent != "." {
		root = path.Join(parent, root)
	}
	return path.Join(root, basename+".manifest.json"), basename, path.Join(root, basename), true
}

func directChild(parent, candidate string) bool {
	prefix := strings.TrimSuffix(parent, "/") + "/"
	child := strings.TrimPrefix(candidate, prefix)
	return child != candidate && child != "" && !strings.Contains(child, "/")
}

func sha256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func narrateDisplayValue(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 80 {
		return "", false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return "", false
		}
	}
	return value, true
}

func narratePace(value string) bool {
	switch value {
	case "slow", "relaxed", "natural", "brisk", "fast":
		return true
	default:
		return false
	}
}

func narrateDuration(ms int64) string {
	seconds := (ms + 500) / 1000
	if seconds < 60 {
		return fmt.Sprintf("%d sec", seconds)
	}
	minutes := seconds / 60
	seconds %= 60
	if minutes < 60 {
		return fmt.Sprintf("%d:%02d", minutes, seconds)
	}
	hours := minutes / 60
	minutes %= 60
	return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
}
