class Ckx < Formula
  desc "Switch between AWS (EKS) and Azure (AKS) clusters from one CLI"
  homepage "https://github.com/eimarfandino/ckx"
  version "0.9.0"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/eimarfandino/ckx/releases/download/v#{version}/ckx_v#{version}_darwin_amd64.zip"
      sha256 "REPLACE_WITH_ARM64_SHA256"
    else
      url "https://github.com/eimarfandino/ckx/releases/download/v#{version}/ckx_v#{version}_darwin_amd64.zip"
      sha256 "REPLACE_WITH_AMD64_SHA256"
    end
  end

  def install
    system "unzip", "-q", Dir["ckx_v*_darwin_*.zip"].first if Dir["*.zip"].any?
    bin.install "ckx"
  end

  test do
    system "#{bin}/ckx", "--help"
  end
end
