// Package launcherbinary reads the version a launcher binary carries inside
// itself, so a release's version and download name come from the binary and
// cannot be typed wrong (user decision 2026-11-11).
//
// Pandora (Windows): the PE version resource. crates/pandora_launcher/build.rs
// writes the release version (PANDORA_RELEASE_VERSION) to the ProductVersion
// and FileVersion strings; a build without it carries no version and is
// rejected. Android: versionName, versionCode and package of the binary
// AndroidManifest.xml (AXML) inside the APK. Everything is parsed in Go from
// the stored object; no external tool runs on the server.
package launcherbinary

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/wachipayox/BootOptimDistribution/internal/semver"
)

// AndroidPackage is the application id the Android updater installs over.
const AndroidPackage = "net.wachiland.launcher"

// Metadata is what a release takes from its binary.
type Metadata struct {
	Version  string `json:"version"`
	FileName string `json:"file_name"`
	// Detail is a short human description for the panel (machine, versionCode…).
	Detail string `json:"detail"`
}

// Error is a rejection with a stable code and a message for the administrator.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func reject(code, format string, args ...interface{}) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// AsError returns the *Error inside err, if any.
func AsError(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

var peMachines = map[string]uint16{
	"windows-x86_64":  0x8664,
	"windows-aarch64": 0xaa64,
}

// Platforms lists the platforms whose binaries can be read, per app.
func Platforms(app string) []string {
	switch app {
	case "pandora":
		return []string{"windows-x86_64", "windows-aarch64"}
	case "android":
		return []string{"android-universal", "android-arm64-v8a", "android-armeabi-v7a", "android-x86", "android-x86_64"}
	}
	return nil
}

func supported(app, platform string) bool {
	for _, p := range Platforms(app) {
		if p == platform {
			return true
		}
	}
	return false
}

// FileName is the download name of a release artifact.
func FileName(app, version, platform string) string {
	ext := ".exe"
	if app == "android" {
		ext = ".apk"
	}
	return "WachilandLauncher-" + version + "-" + platform + ext
}

// Inspect reads the binary of one artifact and returns the release metadata
// it implies, or an *Error explaining why it cannot be published.
func Inspect(app, platform string, r io.ReaderAt, size int64) (Metadata, error) {
	if !supported(app, platform) {
		return Metadata{}, reject("platform_unsupported", "la plataforma %q no se admite para %s; admitidas: %s", platform, app, strings.Join(Platforms(app), ", "))
	}
	var version, detail string
	switch app {
	case "pandora":
		info, err := ReadPE(r, size)
		if err != nil {
			return Metadata{}, err
		}
		if info.Machine != peMachines[platform] {
			return Metadata{}, reject("platform_mismatch", "el ejecutable es para la arquitectura 0x%04x, no para %s", info.Machine, platform)
		}
		version, detail = info.Version, fmt.Sprintf("PE 0x%04x, ProductVersion %s", info.Machine, info.Version)
	case "android":
		info, err := ReadAPK(r, size)
		if err != nil {
			return Metadata{}, err
		}
		if info.Package != AndroidPackage {
			return Metadata{}, reject("package_mismatch", "el APK es del paquete %q, no de %s", info.Package, AndroidPackage)
		}
		version, detail = info.VersionName, fmt.Sprintf("%s, versionCode %d", info.Package, info.VersionCode)
	}
	if _, err := semver.Parse(version); err != nil {
		return Metadata{}, reject("version_not_semver", "la versión del binario %q no es SemVer 2.0", version)
	}
	return Metadata{Version: version, FileName: FileName(app, version, platform), Detail: detail}, nil
}
