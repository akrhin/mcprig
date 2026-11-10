package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sintez/mcprig/internal/config"
	"github.com/sintez/mcprig/internal/manager"
	"github.com/sintez/mcprig/internal/registry"
	"github.com/sintez/mcprig/internal/sandbox"
	"github.com/sintez/mcprig/internal/toolout"
)

// errDoctorProblems — отчёт уже напечатан; main выходит с кодом 1 без лишней
// строки об ошибке.
var errDoctorProblems = errors.New("doctor: есть проблемы")

// doctorLine — одна проверка: ok либо проблема, с пояснением.
type doctorLine struct {
	OK   bool   `json:"ok"`
	Text string `json:"text"`
}

// doctorReport — те же разделы, что печатает текстовый режим.
type doctorReport struct {
	Version  []doctorLine `json:"version"`
	Config   []doctorLine `json:"config"`
	Sets     []doctorLine `json:"sets"`
	Keys     []doctorLine `json:"keys"`
	Secrets  []doctorLine `json:"secrets"`
	Services []doctorLine `json:"services"`
	Problems int          `json:"problems"`
	OK       bool         `json:"ok"`
}

// doctorOptions — всё, что нужно проверкам; в тестах подставляется целиком.
type doctorOptions struct {
	ConfigPath string
	DataDir    string
	BindIP     string
	PanelHost  string
	PanelPort  int
	FirstPort  int
	LastPort   int
	Version    string
	Executable string
}

func newDoctorFlags() (fs *flag.FlagSet, cfgPath *string, asJSON *bool) {
	fs = flag.NewFlagSet("doctor", flag.ContinueOnError)
	cfgPath = fs.String("config", defaultConfigPath(), "путь к servers.json")
	asJSON = fs.Bool("json", false, "машинный вывод")
	return fs, cfgPath, asJSON
}

func runDoctor(args []string) error {
	fs, cfgPath, asJSON := newDoctorFlags()
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	// env панели читаем до сборки отчёта: в нём порт панели и пароль,
	// а порядок «сначала окружение, потом значения по умолчанию» важен.
	envLine, haveEnv := loadPanelEnv(filepath.Dir(*cfgPath))
	_ = envLine
	rep := buildDoctorReport(doctorOptions{
		ConfigPath: *cfgPath,
		DataDir:    stateDir(),
		BindIP:     envDefault("MCPRIG_BIND_IP", "0.0.0.0"),
		PanelHost:  envDefault("MCPRIG_HOST", "127.0.0.1"),
		PanelPort:  envPort("MCPRIG_PORT", 9091),
		FirstPort:  registry.FirstPort,
		LastPort:   registry.LastPort,
		Version:    version,
		Executable: executablePath(),
	}, envLine, haveEnv)

	var out []byte
	if *asJSON {
		body, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			return err
		}
		out = append(body, '\n')
	} else {
		out = renderDoctor(rep)
	}
	if _, err := os.Stdout.Write(out); err != nil {
		return err
	}
	if !rep.OK {
		return errDoctorProblems
	}
	return nil
}

func buildDoctorReport(o doctorOptions, envLine doctorLine, haveEnv bool) doctorReport {
	rep := doctorReport{
		Version: []doctorLine{{OK: true, Text: fmt.Sprintf("версия %s (%s)", o.Version, o.Executable)}},
	}
	reg, regRaw, regErr := readRegistry(o.DataDir)
	var scanRaw []byte
	var cfg *config.Config
	rep.Config, scanRaw, cfg = checkConfig(o.ConfigPath, reg, regRaw)
	rep.Sets, rep.Keys = checkSets(reg, regErr, o.DataDir, o.BindIP)
	if haveEnv {
		// env панели — документированное место для секретов; без него doctor
		// из голого шелла показывал бы все имена как проблемы.
		rep.Secrets = append([]doctorLine{envLine}, checkSecrets(scanRaw, o.DataDir)...)
	} else {
		rep.Secrets = checkSecrets(scanRaw, o.DataDir)
	}
	rep.Services = append(checkSandbox(cfg), checkServices(o, reg, regErr)...)

	for _, lines := range [][]doctorLine{rep.Version, rep.Config, rep.Sets, rep.Keys, rep.Secrets, rep.Services} {
		for _, line := range lines {
			if !line.OK {
				rep.Problems++
			}
		}
	}
	rep.OK = rep.Problems == 0
	return rep
}

func renderDoctor(rep doctorReport) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "mcprig doctor, %s\n", rep.Version[0].Text)
	for _, sec := range []struct {
		title string
		lines []doctorLine
	}{
		{"конфиг", rep.Config},
		{"наборы", rep.Sets},
		{"ключи", rep.Keys},
		{"секреты", rep.Secrets},
		{"служебные", rep.Services},
	} {
		fmt.Fprintf(&b, "\n[%s]\n", sec.title)
		for _, line := range sec.lines {
			prefix, text := "ok: ", line.Text
			if !line.OK {
				prefix = "проблема: "
			} else if warn, ok := strings.CutPrefix(text, "предупреждение: "); ok {
				// Предупреждение — не «ok»: иначе строка читается как «всё хорошо»
				// ровно там, где хозяин должен вмешаться.
				prefix, text = "внимание: ", warn
			}
			fmt.Fprintf(&b, "%s%s\n", prefix, text)
		}
	}
	fmt.Fprintf(&b, "\nитог: %d %s\n", rep.Problems, problemWord(rep.Problems))
	return []byte(b.String())
}

// problemWord — «1 проблема», «2 проблемы», «5 проблем».
func problemWord(n int) string {
	if n%10 == 1 && n%100 != 11 {
		return "проблема"
	}
	if n%10 >= 2 && n%10 <= 4 && (n%100 < 10 || n%100 >= 20) {
		return "проблемы"
	}
	return "проблем"
}

// keyWord — «1 ключ», «2 ключа», «5 ключей».
func keyWord(n int) string {
	if n%10 == 1 && n%100 != 11 {
		return "ключ"
	}
	if n%10 >= 2 && n%10 <= 4 && (n%100 < 10 || n%100 >= 20) {
		return "ключа"
	}
	return "ключей"
}

// readRegistry читает реестр без registry.Open: doctor не имеет права
// переписывать файл (нормализация и сид — дело панели).
func readRegistry(dataDir string) (*registry.Data, []byte, error) {
	path := filepath.Join(dataDir, registry.RegistryFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var data registry.Data
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, raw, fmt.Errorf("реестр %s: %v", path, err)
	}
	return &data, raw, nil
}

// checkConfig сообщает, какой источник конфигурации действует, и отдаёт его
// сырой текст: по нему ищутся ${VAR}.
func checkConfig(path string, reg *registry.Data, regRaw []byte) ([]doctorLine, []byte, *config.Config) {
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		cfg, loadErr := config.Load(path)
		if loadErr != nil {
			return []doctorLine{{false, fmt.Sprintf("конфиг %s: %v", path, loadErr)}}, raw, nil
		}
		enabled := 0
		lines := []doctorLine{}
		for _, srv := range cfg.Servers {
			if srv.Enabled {
				enabled++
			}
			for _, w := range srv.Warnings() {
				lines = append(lines, doctorLine{true, fmt.Sprintf("предупреждение: %s: %s", srv.Name, w)})
			}
		}
		lines = append([]doctorLine{{true, fmt.Sprintf("конфиг %s: %d серверов, %d включено", path, len(cfg.Servers), enabled)}}, lines...)
		return lines, raw, cfg
	case errors.Is(err, fs.ErrNotExist):
		if reg != nil {
			enabled := 0
			for _, srv := range reg.Servers {
				if srv != nil && srv.Enabled {
					enabled++
				}
			}
			return []doctorLine{{true, fmt.Sprintf("конфиг %s не найден, серверы из реестра: %d, %d включено", path, len(reg.Servers), enabled)}}, regRaw, nil
		}
		return []doctorLine{{false, fmt.Sprintf("конфиг %s не найден, реестр недоступен", path)}}, nil, nil
	default:
		return []doctorLine{{false, fmt.Sprintf("конфиг %s: %v", path, err)}}, nil, nil
	}
}

func checkSets(reg *registry.Data, regErr error, dataDir, bindIP string) (sets, keys []doctorLine) {
	if regErr != nil {
		line := doctorLine{false, fmt.Sprintf("реестр %s: %v", filepath.Join(dataDir, registry.RegistryFile), regErr)}
		return []doctorLine{line}, []doctorLine{line}
	}
	if reg == nil || len(reg.Sets) == 0 {
		line := doctorLine{true, fmt.Sprintf("наборов нет (реестр %s)", filepath.Join(dataDir, registry.RegistryFile))}
		return []doctorLine{line}, []doctorLine{line}
	}

	dialHost := loopbackHost(bindIP)
	for _, set := range reg.Sets {
		if set == nil {
			continue
		}
		prefix, active := activeKey(set.Keys)
		switch {
		case active:
			keys = append(keys, doctorLine{true, fmt.Sprintf("%s: %d %s, активный %s", set.ID, len(set.Keys), keyWord(len(set.Keys)), prefix)})
		case set.Open:
			keys = append(keys, doctorLine{true, fmt.Sprintf("%s: %d %s, набор открыт (ключ не нужен)", set.ID, len(set.Keys), keyWord(len(set.Keys)))})
		default:
			keys = append(keys, doctorLine{false, fmt.Sprintf("%s: %d %s, активного ключа нет — набор не стартует", set.ID, len(set.Keys), keyWord(len(set.Keys)))})
		}

		if set.Port < registry.FirstPort || set.Port > registry.LastPort {
			sets = append(sets, doctorLine{false, fmt.Sprintf("%s: порт %d вне диапазона %d–%d", set.ID, set.Port, registry.FirstPort, registry.LastPort)})
			continue
		}
		addr := net.JoinHostPort(dialHost, strconv.Itoa(set.Port))
		if !portOpen(addr) {
			if set.Autostart {
				sets = append(sets, doctorLine{false, fmt.Sprintf("%s: порт %d, autostart, но не слушает %s", set.ID, set.Port, addr)})
			} else {
				sets = append(sets, doctorLine{true, fmt.Sprintf("%s: порт %d, остановлен (autostart выключен)", set.ID, set.Port)})
			}
			continue
		}
		code, err := healthz(addr)
		if err != nil || code != http.StatusOK {
			sets = append(sets, doctorLine{false, fmt.Sprintf("%s: порт %d слушает, /healthz %s", set.ID, set.Port, httpStatus(code, err))})
			continue
		}
		state := "слушает"
		if set.Autostart {
			state += ", autostart"
		}
		sets = append(sets, doctorLine{true, fmt.Sprintf("%s: порт %d, %s, /healthz 200", set.ID, set.Port, state)})
	}
	return sets, keys
}

// loadPanelEnv читает <dir>/env рядом с конфигом и добавляет недостающие
// переменные в окружение процесса. Значения наружу не печатаются.
func loadPanelEnv(dir string) (doctorLine, bool) {
	path := filepath.Join(dir, "env")
	body, err := os.ReadFile(path)
	if err != nil {
		return doctorLine{}, false
	}
	n := 0
	for _, raw := range strings.Split(string(body), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		name, value, found := strings.Cut(line, "=")
		if !found || name == "" {
			continue
		}
		if _, set := os.LookupEnv(name); set {
			continue
		}
		if err := os.Setenv(name, strings.Trim(value, `"'`)); err == nil {
			n++
		}
	}
	return doctorLine{true, fmt.Sprintf("env панели %s: добавлено %d переменных", path, n)}, true
}

func checkSecrets(raw []byte, dataDir string) []doctorLine {
	names := mentionedSecrets(raw)
	if len(names) == 0 {
		return []doctorLine{{true, "переменных ${VAR} в конфиге нет"}}
	}

	var lines []doctorLine
	sec, err := manager.NewSecrets(dataDir)
	if err != nil {
		lines = append(lines, doctorLine{false, fmt.Sprintf("env.json: %v", err)})
	}
	for _, name := range names {
		source := ""
		if sec != nil {
			// Значение отбрасываем: печатаются только имена и источник.
			_, source = sec.Get(name)
		} else if _, ok := os.LookupEnv(name); ok {
			source = "env"
		}
		if source == "" {
			lines = append(lines, doctorLine{false, fmt.Sprintf("%s: не найден (OpenBao, env.json, окружение)", name)})
			continue
		}
		lines = append(lines, doctorLine{true, fmt.Sprintf("%s: %s", name, secretSourceLabel(source))})
	}
	if sec != nil && sec.RemoteError() != "" {
		lines = append(lines, doctorLine{false, fmt.Sprintf("OpenBao: %s", sec.RemoteError())})
	}
	return lines
}

// checkSandbox спрашивает то же, что проверит Wrap: bwrap и prlimit есть,
// userns разрешён. Провал — ошибка только когда песочницу кто-то просит.
func checkSandbox(cfg *config.Config) []doctorLine {
	wanted := false
	if cfg != nil {
		for _, srv := range cfg.Servers {
			if sb := srv.Sandbox; sb != nil && sb.Mode == config.SandboxModeBwrap {
				wanted = true
			}
		}
	}
	if err := sandbox.Probe(); err != nil {
		if wanted {
			return []doctorLine{{false, fmt.Sprintf("песочница: %v (её просят серверы конфига)", err)}}
		}
		return []doctorLine{{true, fmt.Sprintf("предупреждение: песочница: %v", err)}}
	}
	return []doctorLine{{true, "песочница: bwrap запускается, userns разрешён"}}
}

func checkServices(o doctorOptions, reg *registry.Data, regErr error) []doctorLine {
	var lines []doctorLine

	setPorts := map[int]bool{}
	if regErr == nil && reg != nil {
		for _, set := range reg.Sets {
			if set != nil {
				setPorts[set.Port] = true
			}
		}
	}
	var foreign []string
	for port := o.FirstPort; port <= o.LastPort; port++ {
		if setPorts[port] {
			continue
		}
		if portOpen(net.JoinHostPort("127.0.0.1", strconv.Itoa(port))) {
			foreign = append(foreign, strconv.Itoa(port))
		}
	}
	if len(foreign) == 0 {
		lines = append(lines, doctorLine{true, fmt.Sprintf("чужих слушателей в диапазоне %d–%d нет", o.FirstPort, o.LastPort)})
	} else {
		// AllocatePort пропускает занятые порты в системе, поэтому это не проблема.
		lines = append(lines, doctorLine{true, fmt.Sprintf("чужие порты в диапазоне %d–%d: %s (выдача портов их пропускает)", o.FirstPort, o.LastPort, strings.Join(foreign, ", "))})
	}

	if regErr == nil && reg != nil {
		found := false
		for _, set := range reg.Sets {
			if set == nil {
				continue
			}
			path := filepath.Join(o.DataDir, "sets", set.ID, "oauth.json")
			info, err := os.Stat(path)
			switch {
			case err == nil:
				found = true
				lines = append(lines, doctorLine{true, fmt.Sprintf("sets/%s/oauth.json: %s", set.ID, humanBytes(info.Size()))})
			case errors.Is(err, fs.ErrNotExist):
			default:
				lines = append(lines, doctorLine{false, fmt.Sprintf("sets/%s/oauth.json: %v", set.ID, err)})
			}
		}
		if !found {
			lines = append(lines, doctorLine{true, "sets/*/oauth.json нет"})
		}
	}

	lines = append(lines, toolOutLine(o.DataDir))

	addr := net.JoinHostPort(loopbackHost(o.PanelHost), strconv.Itoa(o.PanelPort))
	code, err := healthz(addr)
	switch {
	case err != nil:
		lines = append(lines, doctorLine{false, fmt.Sprintf("панель %s недоступна: %v", addr, err)})
	case code != http.StatusOK:
		lines = append(lines, doctorLine{false, fmt.Sprintf("панель %s: /healthz %d", addr, code)})
	default:
		lines = append(lines, doctorLine{true, fmt.Sprintf("панель %s: /healthz 200", addr)})
	}
	return lines
}

var secretRefRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func mentionedSecrets(raw []byte) []string {
	seen := map[string]bool{}
	for _, m := range secretRefRe.FindAllSubmatch(raw, -1) {
		seen[string(m[1])] = true
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func secretSourceLabel(source string) string {
	switch source {
	case "openbao":
		return "OpenBao"
	case "file":
		return "env.json"
	case "env":
		return "окружение"
	}
	return source
}

func activeKey(keys []registry.Key) (prefix string, ok bool) {
	for _, key := range keys {
		if key.RevokedAt != "" || key.Token == "" {
			continue
		}
		if len(key.Token) > 8 {
			return key.Token[:8], true
		}
		return key.Token, true
	}
	return "", false
}

var doctorClient = &http.Client{Timeout: 2 * time.Second}

func healthz(addr string) (int, error) {
	res, err := doctorClient.Get("http://" + addr + "/healthz")
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	return res.StatusCode, nil
}

func httpStatus(code int, err error) string {
	if err != nil {
		return err.Error()
	}
	return strconv.Itoa(code)
}

func portOpen(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// loopbackHost превращает wildcard адрес в тот, по которому реально ответит
// локальный процесс.
func loopbackHost(host string) string {
	switch host {
	case "", "0.0.0.0":
		return "127.0.0.1"
	case "[::]", "::":
		return "::1"
	}
	return host
}

// toolOutLine describes the saved truncated answers: how many, how big and how
// old the next one to expire is.
func toolOutLine(dataDir string) doctorLine {
	size, count, oldest, err := toolout.Usage(dataDir)
	switch {
	case err == nil:
		line := fmt.Sprintf("tool-out: %d файлов, %s", count, humanBytes(size))
		if count > 0 {
			line += fmt.Sprintf(", самый старый %s", humanAge(time.Since(oldest)))
		}
		return doctorLine{true, line}
	case errors.Is(err, fs.ErrNotExist):
		return doctorLine{true, "tool-out нет"}
	default:
		return doctorLine{false, fmt.Sprintf("tool-out: %v", err)}
	}
}

func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "меньше минуты назад"
	case d < time.Hour:
		return fmt.Sprintf("%d мин назад", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d ч назад", int(d.Hours()))
	default:
		return fmt.Sprintf("%d дн назад", int(d.Hours()/24))
	}
}

func humanBytes(n int64) string {
	const unit = 1024
	switch {
	case n < unit:
		return fmt.Sprintf("%d Б", n)
	case n < unit*unit:
		return fmt.Sprintf("%.1f КБ", float64(n)/unit)
	case n < unit*unit*unit:
		return fmt.Sprintf("%.1f МБ", float64(n)/(unit*unit))
	default:
		return fmt.Sprintf("%.1f ГБ", float64(n)/(unit*unit*unit))
	}
}

func executablePath() string {
	if path, err := os.Executable(); err == nil {
		return path
	}
	return os.Args[0]
}

func envDefault(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func envPort(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			return port
		}
	}
	return def
}
