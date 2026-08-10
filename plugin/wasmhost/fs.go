package wasmhost

import "os"

// osFS implements FS over the host filesystem with NO sandbox boundary.
// The trust model is explicit: whoever can place a .wasm in the plugin
// directory already has the process's capabilities (keyring, exec,
// env) — fs_* adds file access to the same trust domain. Deployments
// that need a boundary substitute their own FS implementation (chroot,
// container, allowlist) via HostAPI.WithFS.
type osFS struct{}

// NewOSFS returns an unrestricted filesystem-backed FS.
func NewOSFS() FS { return osFS{} }

func (osFS) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (osFS) WriteFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}

func (osFS) ReadDir(path string) ([]os.DirEntry, error) {
	return os.ReadDir(path)
}
