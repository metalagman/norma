package appconfig

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

const (
	// CoreConfigFileName is the fallback config file name.
	CoreConfigFileName = "config.yaml"
	relayAppName       = "relay"
	runtimeRootKey     = "norma"
	overridesRootKey   = "profiles"
	defaultProfileName = "default"
)

// RuntimeLoadOptions configures runtime config loading.
type RuntimeLoadOptions struct {
	WorkingDir string
	ConfigDir  string
	Profile    string
}

// AppLoadOptions configures app config loading on top of runtime config.
type AppLoadOptions struct {
	AppName      string
	DefaultsYAML []byte
}

// LoadConfigDocument loads and decodes a full app config document into out.
//
// The selected file is single-source by priority:
//   - general apps: <app>.yaml first, then config.yaml
//   - relay app: .config/relay/config.yaml (or config-dir override targets)
//
// Profile overrides (profiles.<name>) and app env overrides are applied before decode.
func LoadConfigDocument(runtimeOpts RuntimeLoadOptions, opts AppLoadOptions, out any) (string, error) {
	if out == nil {
		return "", fmt.Errorf("output config target is required")
	}

	appName := strings.TrimSpace(opts.AppName)
	if appName == "" {
		return "", fmt.Errorf("app name is required")
	}

	settings, selectedProfile, err := loadResolvedSettings(runtimeOpts, opts)
	if err != nil {
		return "", err
	}

	runtimeSettings, ok := extractAppSection(settings, runtimeRootKey)
	if !ok {
		return "", fmt.Errorf("runtime config key %q is required", runtimeRootKey)
	}
	if err := rejectLegacyRuntimeAppKeys(runtimeSettings); err != nil {
		return "", err
	}
	if err := ValidateSettings(runtimeSettings); err != nil {
		return "", fmt.Errorf("validate runtime config: %w", err)
	}
	if err := DecodeSettings(settings, out); err != nil {
		return "", fmt.Errorf("decode config: %w", err)
	}

	return selectedProfile, nil
}

func loadResolvedSettings(runtimeOpts RuntimeLoadOptions, opts AppLoadOptions) (map[string]any, string, error) {
	appName := strings.TrimSpace(opts.AppName)
	if appName == "" {
		return nil, "", fmt.Errorf("app name is required")
	}

	selectedPath, searchedPaths, err := selectConfigFile(runtimeOpts, appName)
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(selectedPath) == "" {
		return nil, "", fmt.Errorf("runtime config not found; looked for: %s", strings.Join(searchedPaths, ", "))
	}

	v, err := loadConfigViper(selectedPath, opts.DefaultsYAML)
	if err != nil {
		return nil, "", err
	}

	settings := v.AllSettings()
	if settings == nil {
		settings = map[string]any{}
	}
	if err := rejectLegacyRootRuntimeKeys(settings); err != nil {
		return nil, "", err
	}

	selectedProfile, err := applyProfileOverlay(v, settings, runtimeOpts.Profile)
	if err != nil {
		return nil, "", err
	}

	applyAppEnvOverrides(v, appName)

	settings = v.AllSettings()
	if settings == nil {
		settings = map[string]any{}
	}
	if err := rejectLegacyRootRuntimeKeys(settings); err != nil {
		return nil, "", err
	}

	return settings, selectedProfile, nil
}

func loadConfigViper(configPath string, defaultsYAML []byte) (*viper.Viper, error) {
	v := viper.New()
	v.SetConfigType("yaml")

	if len(defaultsYAML) > 0 {
		if err := v.ReadConfig(bytes.NewReader(defaultsYAML)); err != nil {
			return nil, fmt.Errorf("parse yaml in embedded defaults: %w", err)
		}
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read config file %q: %w", configPath, err)
	}

	if len(defaultsYAML) > 0 {
		if err := v.MergeConfig(bytes.NewReader(content)); err != nil {
			return nil, fmt.Errorf("parse config file %q: %w", configPath, err)
		}
	} else {
		if err := v.ReadConfig(bytes.NewReader(content)); err != nil {
			return nil, fmt.Errorf("parse config file %q: %w", configPath, err)
		}
	}

	return v, nil
}

func applyProfileOverlay(v *viper.Viper, settings map[string]any, requestedProfile string) (string, error) {
	selected := strings.TrimSpace(requestedProfile)
	if selected == "" {
		selected = defaultProfileName
	}

	profiles, hasProfiles, err := extractTopLevelProfiles(settings)
	if err != nil {
		return "", err
	}
	if !hasProfiles {
		return selected, nil
	}

	rawOverride, ok := profiles[selected]
	if !ok {
		return "", fmt.Errorf("top-level profile %q not found", selected)
	}
	overrideMap, ok := toStringAnyMap(rawOverride)
	if !ok {
		return "", fmt.Errorf("top-level profiles.%s must be an object", selected)
	}
	if err := rejectLegacyRuntimeKeysInOverride(selected, overrideMap); err != nil {
		return "", err
	}
	if err := v.MergeConfigMap(overrideMap); err != nil {
		return "", fmt.Errorf("merge top-level profiles.%s: %w", selected, err)
	}

	return selected, nil
}

func selectConfigFile(runtimeOpts RuntimeLoadOptions, appName string) (string, []string, error) {
	var searched []string
	if appName == relayAppName {
		searched = searchedRelayConfigPaths(runtimeOpts.WorkingDir, runtimeOpts.ConfigDir)
	} else {
		roots := coreConfigRoots(runtimeOpts.WorkingDir, runtimeOpts.ConfigDir)
		searched = searchedConfigPaths(roots, appName)
	}
	for _, path := range searched {
		if _, err := os.Stat(path); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return "", searched, fmt.Errorf("stat config file %q: %w", path, err)
		}
		return path, searched, nil
	}
	return "", searched, nil
}

func searchedRelayConfigPaths(workingDir, configuredRoot string) []string {
	paths := make([]string, 0, 3)

	if extra := strings.TrimSpace(configuredRoot); extra != "" {
		if !filepath.IsAbs(extra) && workingDir != "" {
			extra = filepath.Join(workingDir, extra)
		}
		paths = append(paths,
			filepath.Join(extra, "relay", CoreConfigFileName),
			filepath.Join(extra, CoreConfigFileName),
		)
	}
	if workingDir != "" {
		paths = append(paths, filepath.Join(workingDir, ".config", "relay", CoreConfigFileName))
	}

	return dedupePaths(paths)
}

func searchedConfigPaths(roots []string, appName string) []string {
	paths := make([]string, 0, len(roots)*2)
	for _, root := range roots {
		paths = append(paths,
			filepath.Join(root, appName+".yaml"),
			filepath.Join(root, CoreConfigFileName),
		)
	}
	return paths
}

func rejectLegacyRootRuntimeKeys(root map[string]any) error {
	legacy := []string{"agents", "mcps", "mcp_servers", "profile", "budgets", "retention"}
	for _, key := range legacy {
		if _, ok := root[key]; ok {
			return fmt.Errorf("legacy top-level key %q is no longer supported; move runtime keys under %q and CLI settings under %q", key, runtimeRootKey, "cli")
		}
	}
	return nil
}

func rejectLegacyRuntimeKeysInOverride(profileName string, override map[string]any) error {
	legacy := []string{"agents", "mcps", "mcp_servers", "profile", "budgets", "retention", "pdca", "planner"}
	for _, key := range legacy {
		if _, ok := override[key]; ok {
			return fmt.Errorf("legacy runtime key %q in top-level profiles.%s is not supported; nest runtime overrides under %q", key, profileName, runtimeRootKey)
		}
	}
	return nil
}

func rejectLegacyRuntimeAppKeys(runtimeSettings map[string]any) error {
	if _, ok := runtimeSettings["mcps"]; ok {
		return fmt.Errorf("runtime key %q.%s is no longer supported; use %q.%s instead", runtimeRootKey, "mcps", runtimeRootKey, "mcp_servers")
	}
	if _, ok := runtimeSettings["profiles"]; ok {
		return fmt.Errorf("runtime key %q.%s is no longer supported; move workflow settings under %q.%s", runtimeRootKey, "profiles", "cli", "pdca")
	}
	if _, ok := runtimeSettings["budgets"]; ok {
		return fmt.Errorf("runtime key %q.%s is no longer supported; move it to %s.budgets", runtimeRootKey, "budgets", "cli")
	}
	if _, ok := runtimeSettings["retention"]; ok {
		return fmt.Errorf("runtime key %q.%s is no longer supported; move it to %s.retention", runtimeRootKey, "retention", "cli")
	}
	return nil
}

func extractTopLevelProfiles(root map[string]any) (map[string]any, bool, error) {
	raw, ok := root[overridesRootKey]
	if !ok || raw == nil {
		return nil, false, nil
	}
	profiles, ok := toStringAnyMap(raw)
	if !ok {
		return nil, false, fmt.Errorf("top-level key %q must be an object", overridesRootKey)
	}
	if len(profiles) == 0 {
		return nil, false, nil
	}
	return profiles, true, nil
}

// DecodeSettings decodes a settings map into a target struct using mapstructure tags.
func DecodeSettings(settings map[string]any, out any) error {
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:           out,
		TagName:          "mapstructure",
		WeaklyTypedInput: true,
		DecodeHook: mapstructure.ComposeDecodeHookFunc(
			mapstructure.StringToSliceHookFunc(","),
		),
	})
	if err != nil {
		return fmt.Errorf("create settings decoder: %w", err)
	}
	if err := decoder.Decode(settings); err != nil {
		return fmt.Errorf("decode settings: %w", err)
	}
	return nil
}

func coreConfigRoots(workingDir, configuredRoot string) []string {
	roots := make([]string, 0, 3)

	if extra := strings.TrimSpace(configuredRoot); extra != "" {
		if !filepath.IsAbs(extra) && workingDir != "" {
			extra = filepath.Join(workingDir, extra)
		}
		roots = append(roots, extra)
	}

	if workingDir != "" {
		roots = append(roots, filepath.Join(workingDir, ".norma"))
	}

	if global := globalConfigRoot(); global != "" {
		roots = append(roots, global)
	}

	return dedupePaths(roots)
}

func globalConfigRoot() string {
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "norma")
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".config", "norma")
}

func dedupePaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		cleaned := filepath.Clean(strings.TrimSpace(p))
		if cleaned == "." || cleaned == "" {
			continue
		}
		if _, exists := seen[cleaned]; exists {
			continue
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}
	return out
}

func applyAppEnvOverrides(v *viper.Viper, appName string) {
	prefix := strings.ToUpper(strings.TrimSpace(appName))
	if prefix == "" {
		return
	}

	settings := v.AllSettings()
	if settings == nil {
		return
	}
	appSettings, ok := extractAppSection(settings, appName)
	if !ok {
		return
	}

	envViper := viper.New()
	envViper.SetEnvPrefix(prefix)
	envViper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	envViper.AutomaticEnv()

	for _, key := range leafPaths(appSettings, "") {
		if !envViper.IsSet(key) {
			continue
		}
		v.Set(appName+"."+key, envViper.Get(key))
	}
}

func leafPaths(m map[string]any, parent string) []string {
	paths := make([]string, 0)
	for key, raw := range m {
		segment := strings.TrimSpace(key)
		if segment == "" {
			continue
		}
		fullKey := segment
		if parent != "" {
			fullKey = parent + "." + segment
		}

		nested, ok := toStringAnyMap(raw)
		if !ok || len(nested) == 0 {
			paths = append(paths, fullKey)
			continue
		}

		paths = append(paths, leafPaths(nested, fullKey)...)
	}
	return paths
}

func extractAppSection(doc map[string]any, appName string) (map[string]any, bool) {
	raw, ok := doc[appName]
	if !ok {
		return nil, false
	}
	section, ok := toStringAnyMap(raw)
	if !ok {
		return nil, false
	}
	return section, true
}

func toStringAnyMap(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	case map[any]any:
		out := make(map[string]any, len(typed))
		for k, v := range typed {
			key, ok := k.(string)
			if !ok {
				return nil, false
			}
			out[key] = v
		}
		return out, true
	default:
		return nil, false
	}
}
