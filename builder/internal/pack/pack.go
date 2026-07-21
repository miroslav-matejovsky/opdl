package pack

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/miroslav-matejovsky/opdl/builder/deployment"
	"github.com/miroslav-matejovsky/opdl/utils/atomicfile"
)

// builderName identifies this tool in release metadata.
const builderName = "opdl"

// platformCmd is the package the builder compiles, relative to the platform
// module root.
const platformCmd = "./cmd"

// deploymentFile is the deployment descriptor filename, both embedded in the
// platform binary and shipped alongside it in a package.
const deploymentFile = "deployment.json"

// Packer builds deployment packages by staging a machine's deployment
// descriptor into the platform's embedded folder and driving the platform's own
// go build.
type Packer struct {
	platformDir string
	embedFile   string
	outputDir   string
	goos        string
	goarch      string
}

// New builds a Packer. platformDir is the platform module root; outputDir is
// where packages are written; goos/goarch cross-compile (empty means host). It
// validates the platform's neutral embedded descriptor exists. Machine builds
// replace it through a Go build overlay and never modify the working tree.
func New(platformDir, outputDir, goos, goarch string) (*Packer, error) {
	absolutePlatformDir, err := filepath.Abs(platformDir)
	if err != nil {
		return nil, fmt.Errorf("resolve platform directory: %w", err)
	}
	embedFile := filepath.Join(absolutePlatformDir, "embedded", deploymentFile)
	if _, err := os.Stat(embedFile); err != nil {
		return nil, fmt.Errorf("find embedded deployment descriptor: %w", err)
	}
	absoluteOutputDir, err := filepath.Abs(outputDir)
	if err != nil {
		return nil, fmt.Errorf("resolve output directory: %w", err)
	}
	return &Packer{
		platformDir: absolutePlatformDir,
		embedFile:   embedFile,
		outputDir:   absoluteOutputDir,
		goos:        goos,
		goarch:      goarch,
	}, nil
}

// Result reports what a machine build produced.
type Result struct {
	Dir    string
	Binary string
	SHA256 string
}

// BuildMachine stages the machine's deployment descriptor into the platform's
// embedded folder, compiles the platform, and assembles the deployment
// package: the binary, a copy of the descriptor, a manifest, release metadata,
// and a checksums file.
func (p *Packer) BuildMachine(ctx context.Context, d deployment.Descriptor) (result *Result, resultErr error) {
	stageDir, err := os.MkdirTemp("", "opdl-build-overlay-")
	if err != nil {
		return nil, fmt.Errorf("create build overlay directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(stageDir); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove build overlay directory: %w", err))
		}
	}()

	overlayPath, err := p.stageOverlay(stageDir, d)
	if err != nil {
		return nil, err
	}

	pkgDir := filepath.Join(p.outputDir, d.Project, d.Site, d.Machine)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		return nil, fmt.Errorf("create package dir %s: %w", pkgDir, err)
	}

	binaryName := d.Machine + p.binaryExt()
	binaryPath := filepath.Join(pkgDir, binaryName)
	if err := p.compile(ctx, binaryPath, overlayPath); err != nil {
		return nil, err
	}

	if err := writeJSON(filepath.Join(pkgDir, deploymentFile), d); err != nil {
		return nil, fmt.Errorf("write deployment descriptor: %w", err)
	}

	sum, err := fileSHA256(binaryPath)
	if err != nil {
		return nil, err
	}
	if err := p.writeMetadata(pkgDir, d, binaryName, sum); err != nil {
		return nil, err
	}

	return &Result{Dir: pkgDir, Binary: binaryName, SHA256: sum}, nil
}

// stageOverlay writes the machine descriptor and the Go overlay that maps the
// neutral embedded descriptor to it for this build only.
func (p *Packer) stageOverlay(stageDir string, d deployment.Descriptor) (string, error) {
	stagedDescriptor := filepath.Join(stageDir, deploymentFile)
	if err := writeJSON(stagedDescriptor, d); err != nil {
		return "", fmt.Errorf("stage deployment descriptor: %w", err)
	}
	overlayPath := filepath.Join(stageDir, "overlay.json")
	overlay := struct {
		Replace map[string]string `json:"Replace"`
	}{Replace: map[string]string{p.embedFile: stagedDescriptor}}
	if err := writeJSON(overlayPath, overlay); err != nil {
		return "", fmt.Errorf("write build overlay: %w", err)
	}
	return overlayPath, nil
}

// compile runs go build for the platform command into out.
func (p *Packer) compile(ctx context.Context, out, overlayPath string) error {
	cmd := exec.CommandContext(ctx, "go", "build", "-overlay", overlayPath, "-o", out, platformCmd)
	cmd.Dir = p.platformDir
	cmd.Env = p.buildEnv()
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go build: %w\n%s", err, output)
	}
	return nil
}

// writeMetadata writes the manifest, release metadata, and checksums file.
func (p *Packer) writeMetadata(pkgDir string, d deployment.Descriptor, binary, sum string) error {
	now := time.Now().UTC()
	primary, standby := launches(!d.Slots.Standby.Disabled)
	man := Manifest{
		Project:     d.Project,
		Site:        d.Site,
		Machine:     d.Machine,
		Role:        d.Role,
		Platform:    d.Platform,
		Binary:      binary,
		Services:    d.Services,
		Deployment:  deploymentFile,
		Primary:     primary,
		Standby:     standby,
		GeneratedAt: now,
	}
	rel := Release{
		Builder:      builderName,
		BuiltAt:      now,
		OS:           p.effectiveGOOS(),
		Arch:         p.effectiveGOARCH(),
		BinarySHA256: sum,
	}

	if err := writeJSON(filepath.Join(pkgDir, "manifest.json"), man); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(pkgDir, "release.json"), rel); err != nil {
		return err
	}
	return p.writeChecksums(pkgDir, binary, sum)
}

// writeChecksums writes a checksums.txt covering the binary and the deployment
// descriptor.
func (p *Packer) writeChecksums(pkgDir, binary, binarySum string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s\n", binarySum, binary)
	sum, err := fileSHA256(filepath.Join(pkgDir, deploymentFile))
	if err != nil {
		return err
	}
	fmt.Fprintf(&b, "%s  %s\n", sum, deploymentFile)
	return atomicfile.WriteFile(filepath.Join(pkgDir, "checksums.txt"), []byte(b.String()), 0o644)
}

func (p *Packer) buildEnv() []string {
	env := os.Environ()
	if p.goos != "" {
		env = append(env, "GOOS="+p.goos)
	}
	if p.goarch != "" {
		env = append(env, "GOARCH="+p.goarch)
	}
	return env
}

func (p *Packer) effectiveGOOS() string {
	if p.goos != "" {
		return p.goos
	}
	return runtime.GOOS
}

func (p *Packer) effectiveGOARCH() string {
	if p.goarch != "" {
		return p.goarch
	}
	return runtime.GOARCH
}

// binaryExt returns the executable extension for the target OS.
func (p *Packer) binaryExt() string {
	if p.effectiveGOOS() == "windows" {
		return ".exe"
	}
	return ""
}
