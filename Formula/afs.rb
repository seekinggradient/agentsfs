class Afs < Formula
  desc "Shared filesystem for agent context"
  homepage "https://agentsfs.ai"
  head "https://github.com/seekinggradient/agentsfs.git", branch: "main"

  depends_on "go" => :build
  depends_on "git"

  def install
    # No -X for buildinfo.Version: this is a head-only formula, so `version`
    # interpolates to "HEAD", and buildinfo.CompareVersions parses each dot-part
    # with Atoi and swallows the error to 0 — "HEAD" would sort below every
    # release and `afs update` would report a fresh install as permanently out
    # of date. The checked-in default in internal/buildinfo is already correct
    # for source builds; goreleaser injects the real tag for released binaries.
    system "go", "build", "-trimpath", "-ldflags", "-s -w", "-o", bin/"afs", "./cmd/afs"
  end

  test do
    assert_match "afs ", shell_output("#{bin}/afs version")
  end
end
