class Afs < Formula
  desc "Portable, user-owned memory filesystem for AI agents"
  homepage "https://agentsfs.ai"
  head "https://github.com/seekinggradient/agentsfs.git", branch: "main"

  depends_on "go" => :build
  depends_on "git"

  def install
    system "go", "build", "-trimpath", "-ldflags", "-s -w", "-o", bin/"afs", "./cmd/afs"
  end

  test do
    assert_match "afs ", shell_output("#{bin}/afs version")
  end
end
