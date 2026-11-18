package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/akrhin/mcprig/internal/config"
	"github.com/akrhin/mcprig/internal/manager"
	"github.com/akrhin/mcprig/internal/registry"
	"github.com/akrhin/mcprig/internal/sandbox"
	"github.com/akrhin/mcprig/internal/toolout"
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
	PublicURL  string
	FirstPort  int
	LastPort   int
	Version    string
	Executable string
	// AuthOff и HasPassword нужны одной проверке: панель запущена без пароля
	// (MCPRIG_AUTH=off и ни пароля, ни его хеша). Это предупреждение, не проблема.
	AuthOff     bool
	HasPassword bool
}

func newDoctorFlags() (fs *flag.FlagSet, cfgPath *string, asJSON *bool) {
	fs = flag.NewFlagSet("doctor", flag.ContinueOnError)
	cfgPath = fs.String("config", defaultConfigPath(), "путь к файлу servers.json")
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
		PublicURL:  strings.TrimSpace(os.Getenv("MCPRIG_PUBLIC_URL")),
		FirstPort:  registry.FirstPort,
		LastPort:   registry.LastPort,
		Version:    version,
		Executable: executablePath(),
		AuthOff:    os.Getenv("MCPRIG_AUTH") == "off",
		HasPassword: strings.TrimSpace(os.Getenv("MCPRIG_PASSWORD")) != "" ||
			strings.TrimSpace(os.Getenv("MCPRIG_PASSWORD_HASH")) != "",
	}, envLine, haveEnv)

	var out []byte
	if *asJSON {
		body, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			return err
		}
		body = append(body, '\n')
		out = body
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
	// Пустая установка — законное «ещё не настроено», а не поломка: проверять
	// нечего, и чужая панель с env пользователя к ней отношения не имеют.
	if installEmpty(o.ConfigPath, regErr) {
		rep.Config = []doctorLine{{OK: true, Text: fmt.Sprintf(
			"установка пуста: конфиг %s и реестр %s не созданы, запустите mcprig init",
			o.ConfigPath, filepath.Join(o.DataDir, registry.RegistryFile))}}
		// Пустые разделы — массивы, а не null: потребитель -json не должен
		// отличать «данных нет» от «не считали».
		rep.Sets, rep.Keys = []doctorLine{}, []doctorLine{}
		rep.Secrets, rep.Services = []doctorLine{}, []doctorLine{}
		rep.OK = true
		return rep
	}
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
	rep.Services = append([]doctorLine{checkRedirect(o)}, rep.Services...)

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
		if len(sec.lines) == 0 {
			continue
		}
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
	fmt.Fprintf(&b, "\nитог: %d %s\n", rep.Problems, pluralBy(rep.Problems, "проблема", "проблемы", "проблем"))
	return []byte(b.String())
}

// dec — число с одним знаком после запятой: «1,5», как принято в русском выводе.
func dec(v float64) string {
	return strings.Replace(fmt.Sprintf("%.1f", v), ".", ",", 1)
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
		return nil, raw, fmt.Errorf("реестр %s: %w", path, err)
	}
	return &data, raw, nil
}

// installEmpty отличает «данных ещё нет» от «данные есть, но плохи»: пусто —
// только когда нет и реестра, и конфига. Битый реестр остаётся проблемой.
func installEmpty(configPath string, regErr error) bool {
	if !errors.Is(regErr, fs.ErrNotExist) {
		return false
	}
	_, err := os.Stat(configPath)
	return errors.Is(err, fs.ErrNotExist)
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
			return []doctorLine{{true, fmt.Sprintf("конфиг %s не найден; серверы взяты из реестра: %d, включено %d", path, len(reg.Servers), enabled)}}, regRaw, nil
		}
		return []doctorLine{{false, fmt.Sprintf("конфиг %s не найден, реестр недоступен", path)}}, nil, nil
	default:
		return []doctorLine{{false, fmt.Sprintf("конфиг %s: %v", path, err)}}, nil, nil
	}
}

func checkSets(reg *registry.Data, regErr error, dataDir, bindIP string) (sets, keys []doctorLine) {
	if regErr != nil {
		// readRegistry уже назвал файл: второй «реестр <путь>:» — дубль.
		line := doctorLine{false, regErr.Error()}
		return []doctorLine{line}, []doctorLine{line}
	}
	if reg == nil || len(reg.Sets) == 0 {
		line := doctorLine{true, fmt.Sprintf("наборов нет (реестр %s)", filepath.Join(dataDir, registry.RegistryFile))}
		return []doctorLine{line}, []doctorLine{line}
	}

	dialHost := manager.LoopbackAddr(bindIP)
	for _, set := range reg.Sets {
		if set == nil {
			continue
		}
		prefix, active := activeKey(set.Keys)
		switch {
		case active:
			keys = append(keys, doctorLine{true, fmt.Sprintf("%s: ключей %d, активный — %s", set.ID, len(set.Keys), prefix)})
		case set.Open:
			keys = append(keys, doctorLine{true, fmt.Sprintf("%s: %d %s, набор открыт (ключ не нужен)", set.ID, len(set.Keys), pluralBy(len(set.Keys), "ключ", "ключа", "ключей"))})
		default:
			keys = append(keys, doctorLine{false, fmt.Sprintf("%s: %d %s, активного ключа нет — набор не стартует", set.ID, len(set.Keys), pluralBy(len(set.Keys), "ключ", "ключа", "ключей"))})
		}

		if set.Port < registry.FirstPort || set.Port > registry.LastPort {
			sets = append(sets, doctorLine{false, fmt.Sprintf("%s: порт %d вне диапазона %d–%d", set.ID, set.Port, registry.FirstPort, registry.LastPort)})
			continue
		}
		addr := net.JoinHostPort(dialHost, strconv.Itoa(set.Port))
		if !portOpen(addr) {
			if set.Autostart {
				sets = append(sets, doctorLine{false, fmt.Sprintf("%s: порт %d: автозапуск включён, но порт не слушается", set.ID, set.Port)})
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

// loadPanelEnv читает panel.env (документированное место секретов) и legacy
// env рядом с конфигом и добавляет недостающие переменные в окружение
// процесса. Значения наружу не печатаются; первое найденное значение выигрывает.
func loadPanelEnv(dir string) (doctorLine, bool) {
	var paths []string
	added := 0
	for _, name := range []string{"panel.env", "env"} {
		path := filepath.Join(dir, name)
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		paths = append(paths, path)
		for key, value := range parseEnvFile(string(body)) {
			if _, set := os.LookupEnv(key); set {
				continue
			}
			if err := os.Setenv(key, value); err == nil {
				added++
			}
		}
	}
	if len(paths) == 0 {
		return doctorLine{}, false
	}
	return doctorLine{true, fmt.Sprintf("env панели %s: добавлено %d %s", strings.Join(paths, ", "), added, pluralBy(added, "переменная", "переменные", "переменных"))}, true
}

// parseEnvFile разбирает подмножество systemd EnvironmentFile, которого
// хватает panel.env: «KEY=value», необязательный «export », комментарии
// целой строкой, пробелы вокруг имени. Продолжения строк через «\» и
// escape \n/\t не поддерживаются.
func parseEnvFile(body string) map[string]string {
	out := make(map[string]string)
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		name, value, found := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !found || name == "" {
			continue
		}
		out[name] = unquoteEnvValue(strings.TrimSpace(value))
	}
	return out
}

// unquoteEnvValue снимает одну пару кавычек: внутри двойных раскрываются \"
// и \\, как в systemd; незакрытая кавычка остаётся частью значения. Других
// escape-последовательностей парсер не знает.
func unquoteEnvValue(value string) string {
	if len(value) < 2 {
		return value
	}
	q := value[0]
	if (q != '"' && q != '\'') || value[len(value)-1] != q {
		return value
	}
	inner := value[1 : len(value)-1]
	if q == '\'' {
		return inner
	}
	inner = strings.ReplaceAll(inner, `\"`, `"`)
	return strings.ReplaceAll(inner, `\\`, `\`)
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
			lines = append(lines, doctorLine{false, fmt.Sprintf("%s: не найден ни в OpenBao, ни в env.json, ни в окружении — проверьте BAO_ADDR", name)})
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
			return []doctorLine{{false, fmt.Sprintf("песочница: %v — её просят серверы конфигурации", err)}}
		}
		return []doctorLine{{true, fmt.Sprintf("песочница: %v", err)}}
	}
	return []doctorLine{{true, "песочница: bwrap запускается, userns разрешён"}}
}

// checkRedirect печатает эффективный адрес приёма кода и ругается, когда он
// ведёт в пустоту: провайдер отвергнет регистрацию, а человек — ссылку.
func checkRedirect(o doctorOptions) doctorLine {
	if o.PublicURL != "" {
		if err := config.ValidatePublicURL(o.PublicURL); err != nil {
			return doctorLine{false, fmt.Sprintf("redirect OAuth: %v", err)}
		}
	}
	redirect, err := manager.CallbackURL(o.PublicURL, o.PanelHost, o.PanelPort)
	if err != nil {
		return doctorLine{false, fmt.Sprintf("redirect OAuth: %v", err)}
	}
	u, err := url.Parse(redirect)
	if err != nil {
		return doctorLine{false, fmt.Sprintf("redirect OAuth %q: %v", redirect, err)}
	}
	host := u.Hostname()
	switch {
	case host == "":
		return doctorLine{false, fmt.Sprintf("redirect OAuth %s: нет хоста", redirect)}
	case u.Port() == "0":
		return doctorLine{false, fmt.Sprintf("redirect OAuth %s: порт 0", redirect)}
	case u.Scheme == "https" && (strings.EqualFold(host, "localhost") || (net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback())):
		return doctorLine{false, fmt.Sprintf("redirect OAuth %s: loopback при https недостижим снаружи", redirect)}
	}
	return doctorLine{true, "redirect OAuth: " + redirect}
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

	addr := net.JoinHostPort(manager.LoopbackAddr(o.PanelHost), strconv.Itoa(o.PanelPort))
	code, err := healthz(addr)
	switch {
	case err != nil:
		lines = append(lines, doctorLine{false, fmt.Sprintf("панель %s недоступна: %v", addr, err)})
	case code != http.StatusOK:
		lines = append(lines, doctorLine{false, fmt.Sprintf("панель %s: /healthz %d", addr, code)})
	default:
		lines = append(lines, doctorLine{true, fmt.Sprintf("панель %s: /healthz 200", addr)})
	}
	if o.AuthOff && !o.HasPassword {
		lines = append(lines, doctorLine{true, "предупреждение: панель без пароля (MCPRIG_AUTH=off) — доступна всем, кто дотянется до порта; задайте MCPRIG_PASSWORD"})
	}
	return lines
}

func mentionedSecrets(raw []byte) []string {
	names := config.EnvRefs(string(raw))
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
	// Отмены у doctor нет: срок задаёт таймаут клиента.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+addr+"/healthz", nil)
	if err != nil {
		return 0, err
	}
	res, err := doctorClient.Do(req)
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
	conn, err := (&net.Dialer{Timeout: 500 * time.Millisecond}).DialContext(context.Background(), "tcp", addr)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// toolOutLine describes the saved truncated answers: how many, how big and how
// old the next one to expire is.
func toolOutLine(dataDir string) doctorLine {
	size, count, oldest, err := toolout.Usage(dataDir)
	switch {
	case err == nil:
		line := fmt.Sprintf("tool-out: %d %s, %s", count, pluralBy(count, "файл", "файла", "файлов"), humanBytes(size))
		if count > 0 {
			line += fmt.Sprintf(", самый старый %s", humanAge(time.Since(oldest)))
		}
		return doctorLine{true, line}
	case errors.Is(err, fs.ErrNotExist):
		return doctorLine{true, "tool-out: файлов нет"}
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
		return dec(float64(n)/unit) + " КБ"
	case n < unit*unit*unit:
		return dec(float64(n)/(unit*unit)) + " МБ"
	default:
		return dec(float64(n)/(unit*unit*unit)) + " ГБ"
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
