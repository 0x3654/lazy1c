// Package config загружает описание RAS-кластеров для lazy1c.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"
)


// DefaultTOML — шаблон конфига, создаваемого при первом запуске без файла
// (синхронизировать с lazy1c.example.toml).
const DefaultTOML = `# lazy1c configuration. One [[cluster]] per administration endpoint.
# Docs: https://github.com/0x3654/lazy1c#configuration

refresh_interval = 5   # seconds between polls
command_timeout  = 10  # seconds per request
confirm_on_quit  = false

[theme]
# Base ANSI slots (0-15): follows the terminal palette (like lazydocker).
border_active   = "2"
border_inactive = ""
selected_bg     = "4"
selected_fg     = "0"
hints           = "4"

[[cluster]]
name    = "my cluster"
address = "ras01.example.com:1545"   # RAS port (8.3/8.5); engine=auto falls back to MMC :1540

# [[cluster]]
# name    = "legacy 8.2"
# address = "8.2-server:1540"        # ragent port (8.2 has no RAS)
# engine  = "82"
`

// WriteDefault записывает шаблон конфига по пути path (не перезаписывает
// существующий). Вызывается при первом запуске, когда конфиг не найден.
func WriteDefault(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil // уже есть — не трогаем
	}
	return os.WriteFile(path, []byte(DefaultTOML), 0o644)
}

// Cluster — один RAS-эндпоинт (обычно один сервер 1С с одним кластером).
type Cluster struct {
	Name    string `toml:"name"`
	Address string `toml:"address"` // host:port службы RAS
	User    string `toml:"user"`    // необязательно: администратор кластера
	Pwd     string `toml:"pwd"`     // поддерживает ${ENV} подстановку
	IbUser  string `toml:"ib_user"` // необязательно: администратор информационных баз
	IbPwd   string `toml:"ib_pwd"`  // для баз со своим администратором (как --infobase-user у rac)
	Engine  string `toml:"engine"`  // "auto" (по приветствию), "ras" (8.3+), "82" (ragent 8.2)
}

// Theme — цвета интерфейса (ANSI-коды 0-255).
// Дефолт — палитра lazydocker: зелёная активная рамка, синяя строка-выбор,
// синие подсказки.
type Theme struct {
	BorderActive   string `toml:"border_active"`   // рамка активной панели
	BorderInactive string `toml:"border_inactive"` // рамка остальных панелей
	SelectedBg     string `toml:"selected_bg"`     // фон выбранной строки (курсор)
	SelectedFg     string `toml:"selected_fg"`     // текст выбранной строки
	Hints          string `toml:"hints"`           // текст подсказок/статуса
}

// Config — корневой конфиг.
type Config struct {
	RefreshInterval int       `toml:"refresh_interval"` // секунды
	CommandTimeout  int       `toml:"command_timeout"`  // секунды
	ConfirmOnQuit   bool      `toml:"confirm_on_quit"`  // подтверждать выход
	ReadOnly        bool      `toml:"read_only"`        // только просмотр: без изменений
	Theme           Theme     `toml:"theme"`
	Clusters        []Cluster `toml:"cluster"`
}

// Load ищет конфиг по пути, в текущем каталоге, затем в ~/.config/lazy1c/.
func Load(path string) (*Config, error) {
	candidates := []string{}
	if path != "" {
		candidates = append(candidates, path)
	} else {
		candidates = append(candidates,
			"lazy1c.toml",
			filepath.Join(homeDir(), ".config", "lazy1c", "lazy1c.toml"),
			"1cras.toml", // легаси-имя до переименования
			filepath.Join(homeDir(), ".config", "1cras", "1cras.toml"),
		)
	}
	for _, c := range candidates {
		data, err := os.ReadFile(c)
		if err != nil {
			continue
		}
		cfg, err := parse(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c, err)
		}
		return cfg, nil
	}
	return nil, fmt.Errorf("конфиг не найден: %s, ./lazy1c.toml, ~/.config/lazy1c/lazy1c.toml", path)
}

func parse(data []byte) (*Config, error) {
	cfg := &Config{RefreshInterval: 5, CommandTimeout: 10}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	if len(cfg.Clusters) == 0 {
		return nil, fmt.Errorf("в конфиге нет ни одного [[cluster]]")
	}
	for i := range cfg.Clusters {
		c := &cfg.Clusters[i]
		c.Pwd = os.ExpandEnv(c.Pwd)
		c.IbPwd = os.ExpandEnv(c.IbPwd)
		if c.Name == "" {
			c.Name = c.Address
		}
	}
	return cfg, nil
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}
