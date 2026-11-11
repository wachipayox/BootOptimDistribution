package launcherbinary

import (
	"bytes"
	"testing"
)

func inspect(app, platform string, data []byte) (Metadata, error) {
	return Inspect(app, platform, bytes.NewReader(data), int64(len(data)))
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	e, ok := AsError(err)
	if !ok || e.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

func TestPandoraPE(t *testing.T) {
	good := SyntheticPE(0x8664, map[string]string{"ProductName": "Wachiland Launcher", "ProductVersion": "1.0.0", "FileVersion": "1.0.0"})
	meta, err := inspect("pandora", "windows-x86_64", good)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Version != "1.0.0" || meta.FileName != "WachilandLauncher-1.0.0-windows-x86_64.exe" {
		t.Fatalf("metadata = %+v", meta)
	}
	pre := SyntheticPE(0x8664, map[string]string{"ProductVersion": "1.1.0-rc.1+abc"})
	if meta, err := inspect("pandora", "windows-x86_64", pre); err != nil || meta.Version != "1.1.0-rc.1+abc" {
		t.Fatalf("prerelease: %+v %v", meta, err)
	}
	_, err = inspect("pandora", "windows-aarch64", good)
	wantCode(t, err, "platform_mismatch")
	_, err = inspect("pandora", "linux-x86_64", good)
	wantCode(t, err, "platform_unsupported")
	_, err = inspect("pandora", "windows-x86_64", SyntheticPE(0x8664, nil))
	wantCode(t, err, "version_missing")
	_, err = inspect("pandora", "windows-x86_64", SyntheticPE(0x8664, map[string]string{"ProductName": "x"}))
	wantCode(t, err, "version_missing")
	_, err = inspect("pandora", "windows-x86_64", SyntheticPE(0x8664, map[string]string{"ProductVersion": "0.1.0-dev"}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = inspect("pandora", "windows-x86_64", SyntheticPE(0x8664, map[string]string{"ProductVersion": "1.0"}))
	wantCode(t, err, "version_not_semver")
	_, err = inspect("pandora", "windows-x86_64", SyntheticPE(0x8664, map[string]string{"ProductVersion": "1.0.0", "FileVersion": "1.0.1"}))
	wantCode(t, err, "version_conflict")
	_, err = inspect("pandora", "windows-x86_64", SyntheticAPK(AndroidPackage, "1.0.0", 30))
	wantCode(t, err, "not_pe")
}

func TestAndroidAPK(t *testing.T) {
	meta, err := inspect("android", "android-universal", SyntheticAPK(AndroidPackage, "1.0.0", 30))
	if err != nil {
		t.Fatal(err)
	}
	if meta.Version != "1.0.0" || meta.FileName != "WachilandLauncher-1.0.0-android-universal.apk" || meta.Detail != AndroidPackage+", versionCode 30" {
		t.Fatalf("metadata = %+v", meta)
	}
	_, err = inspect("android", "android-universal", SyntheticAPK("com.example.other", "1.0.0", 30))
	wantCode(t, err, "package_mismatch")
	_, err = inspect("android", "android-universal", SyntheticAPK(AndroidPackage, "", 30))
	wantCode(t, err, "version_missing")
	_, err = inspect("android", "android-universal", SyntheticAPK(AndroidPackage, "0.1.28-alpha.x.", 30))
	wantCode(t, err, "version_not_semver")
	_, err = inspect("android", "windows-x86_64", SyntheticAPK(AndroidPackage, "1.0.0", 30))
	wantCode(t, err, "platform_unsupported")
	_, err = inspect("android", "android-universal", []byte("not a zip"))
	wantCode(t, err, "not_apk")
	_, err = inspect("android", "android-universal", SyntheticPE(0x8664, map[string]string{"ProductVersion": "1.0.0"}))
	wantCode(t, err, "not_apk")
}

// Truncated or corrupted inputs must be rejected, never panic.
func TestCorruptInputsDoNotPanic(t *testing.T) {
	inputs := [][]byte{
		SyntheticPE(0x8664, map[string]string{"ProductVersion": "1.0.0"}),
		SyntheticAXML(AndroidPackage, "1.0.0", 30),
	}
	for _, input := range inputs {
		for cut := 0; cut < len(input); cut += 7 {
			b := append([]byte(nil), input[:cut]...)
			_, _ = ReadPE(bytes.NewReader(b), int64(len(b)))
			_, _ = parseAXMLManifest(b)
		}
		for i := 0; i < len(input); i += 3 {
			b := append([]byte(nil), input...)
			b[i] ^= 0xff
			_, _ = ReadPE(bytes.NewReader(b), int64(len(b)))
			_, _ = parseAXMLManifest(b)
		}
	}
}
