package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/elazarl/goproxy"
)

var (
	proxy      *goproxy.ProxyHttpServer
	server     *http.Server
	isRunning  bool
	cacheDir   = getCacheDir()
	mu         sync.Mutex
	reqCount   int
	cacheHits  int
	statsLabel *widget.Label
	startBtn   *widget.Button
)

func init() {
	os.MkdirAll(cacheDir, 0755)
}

func hashURL(url string) string {
	h := uint32(2166136261)
	for _, c := range url {
		h *= 16777619
		h ^= uint32(c)
	}
	return fmt.Sprintf("%d", h)
}

func getCachePath(url string) string {
	return filepath.Join(cacheDir, hashURL(url))
}

func startProxy() {
	proxy = goproxy.NewProxyHttpServer()
	proxy.Verbose = false

	// Генерируем корневой сертификат при первом запуске
	ca, err := tls.LoadX509KeyPair("ca.crt", "ca.key")
	if err != nil {
		log.Println("Генерируем корневой сертификат...")
		generateCA()
		ca, _ = tls.LoadX509KeyPair("ca.crt", "ca.key")
	}

	// Настраиваем MITM для HTTPS
	goproxy.GoproxyCa = ca

	// AI-сервисы исключаем из MITM (они используют WebSocket/SSE)
	aiDomains := []string{
		"chatgpt.com", "openai.com", "anthropic.com", "claude.ai",
		"deepseek.com", "qwen.ai", "gemini.google.com", "copilot.microsoft.com",
	}
	proxy.OnRequest().HandleConnectFunc(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		for _, domain := range aiDomains {
			if strings.HasSuffix(host, domain) {
				return goproxy.OkConnect, host
			}
		}
		return goproxy.MitmConnect, host
	})

	proxy.OnRequest().DoFunc(func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		url := r.URL.String()
		cachePath := getCachePath(url)

		mu.Lock()
		reqCount++
		mu.Unlock()

		// Проверяем кэш
		if data, err := os.ReadFile(cachePath); err == nil {
			mu.Lock()
			cacheHits++
			mu.Unlock()
			log.Printf("[HIT] %s (из кэша)", url)

			resp := &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(data)),
				Header:     make(http.Header),
			}
			resp.Header.Set("X-Cache", "HIT")
			return r, resp
		}

		log.Printf("[FETCH] %s (идём в интернет)", url)

		// Создаем новый запрос с чистым контекстом
		newReq := r.Clone(context.Background())
		newReq.RequestURI = ""
		tr := &http.Transport{
			Proxy:           nil,
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
		client := &http.Client{
			Transport: tr,
			Timeout:   30 * time.Second,
		}
		resp, err := client.Do(newReq)
		if err != nil {
			log.Printf("[ERROR] HTTP запрос ошибка: %v", err)
			return r, goproxy.NewResponse(r, goproxy.ContentTypeText, http.StatusBadGateway, "Bad Gateway")
		}

		// Сохраняем ответ в кэш (только успешные)
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				log.Printf("[ERROR] ReadAll ошибка: %v", err)
				return r, resp
			}

			if err := os.WriteFile(cachePath, body, 0644); err != nil {
				log.Printf("[ERROR] WriteFile ошибка: %v", err)
			} else {
				log.Printf("[SAVE] %s (%d байт)", url, len(body))
			}
			resp.Body = io.NopCloser(bytes.NewReader(body))
		}

		return r, resp
	})

	server = &http.Server{Addr: ":8080", Handler: proxy}
	go func() {
		log.Printf("Прокси запущен на порту 8080")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Ошибка сервера: %v", err)
		}
	}()
}
func stopProxy() {
	if server != nil {
		server.Close()
		server = nil
	}
}

// Настройка системного прокси
func setSystemProxy(enable bool) error {
	switch runtime.GOOS {
	case "linux":
		return setLinuxProxy(enable)
	case "windows":
		return setWindowsProxy(enable)
	case "darwin":
		return setMacOSProxy(enable)
	default:
		return fmt.Errorf("неподдерживаемая ОС: %s", runtime.GOOS)
	}
}

func setLinuxProxy(enable bool) error {
	if enable {
		cmds := [][]string{
			{"gsettings", "set", "org.gnome.system.proxy", "mode", "manual"},
			{"gsettings", "set", "org.gnome.system.proxy.http", "host", "127.0.0.1"},
			{"gsettings", "set", "org.gnome.system.proxy.http", "port", "8080"},
			{"gsettings", "set", "org.gnome.system.proxy.https", "host", "127.0.0.1"},
			{"gsettings", "set", "org.gnome.system.proxy.https", "port", "8080"},
		}
		for _, cmd := range cmds {
			exec.Command(cmd[0], cmd[1:]...).Run()
		}
		os.Setenv("http_proxy", "http://127.0.0.1:8080")
		os.Setenv("https_proxy", "http://127.0.0.1:8080")
		log.Println("✅ Системный прокси включен (Linux)")
	} else {
		exec.Command("gsettings", "set", "org.gnome.system.proxy", "mode", "none").Run()
		os.Unsetenv("http_proxy")
		os.Unsetenv("https_proxy")
		log.Println("⛔ Системный прокси выключен (Linux)")
	}
	return nil
}

func setWindowsProxy(enable bool) error {
	const regPath = `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	if enable {
		exec.Command("reg", "add", regPath, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "1", "/f").Run()
		exec.Command("reg", "add", regPath, "/v", "ProxyServer", "/t", "REG_SZ", "/d", "127.0.0.1:8080", "/f").Run()
		log.Println("✅ Системный прокси включен (Windows)")
	} else {
		exec.Command("reg", "add", regPath, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "0", "/f").Run()
		log.Println("⛔ Системный прокси выключен (Windows)")
	}
	return nil
}

func setMacOSProxy(enable bool) error {
	out, err := exec.Command("networksetup", "-listallnetworkservices").Output()
	if err != nil {
		return err
	}
	lines := bytes.Split(out, []byte("\n"))
	if len(lines) < 2 {
		return fmt.Errorf("не найдены сетевые интерфейсы")
	}
	service := string(bytes.TrimSpace(lines[1]))
	if bytes.HasPrefix(lines[1], []byte("*")) && len(lines) > 2 {
		service = string(bytes.TrimSpace(lines[2]))
	}

	if enable {
		exec.Command("networksetup", "-setwebproxy", service, "127.0.0.1", "8080").Run()
		exec.Command("networksetup", "-setsecurewebproxy", service, "127.0.0.1", "8080").Run()
		log.Printf("✅ Системный прокси включен (macOS, %s)\n", service)
	} else {
		exec.Command("networksetup", "-setwebproxystate", service, "off").Run()
		exec.Command("networksetup", "-setsecurewebproxystate", service, "off").Run()
		log.Printf("⛔ Системный прокси выключен (macOS, %s)\n", service)
	}
	return nil
}
func getCacheDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "cache"
	}
	dir := filepath.Join(home, ".microproxy", "cache")
	os.MkdirAll(dir, 0755)
	return dir
}

func generateCA() {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "MicroProxy CA"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	certDER, _ := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	os.WriteFile("ca.crt", certPEM, 0644)
	os.WriteFile("ca.key", keyPEM, 0600)
	log.Println("✅ Корневой сертификат создан: ca.crt")
	log.Println("⚠️  Установи ca.crt в систему (см. инструкцию)")
}

func main() {
	a := app.New()
	w := a.NewWindow("MicroProxy - Кэширующий прокси")
	w.SetIcon(resourceIcon256Png)

	title := widget.NewLabelWithStyle("MicroProxy v0.3", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

	startBtn = widget.NewButton("Включить прокси", func() {
		if !isRunning {
			startProxy()
			isRunning = true
			setSystemProxy(true)
			startBtn.SetText("Выключить прокси")
			startBtn.Importance = widget.DangerImportance
			go updateStatsLoop()
		} else {
			stopProxy()
			isRunning = false
			setSystemProxy(false)
			startBtn.SetText("Включить прокси")
			startBtn.Importance = widget.SuccessImportance
			fyne.Do(func() {
				statsLabel.SetText("Прокси выключен")
			})
		}
	})
	startBtn.Importance = widget.SuccessImportance

	statsLabel = widget.NewLabel("Прокси выключен")
	info := widget.NewLabel("Системный прокси настраивается автоматически")
	info.Wrapping = fyne.TextWrapWord

	w.SetContent(container.NewVBox(
		title,
		widget.NewSeparator(),
		startBtn,
		statsLabel,
		widget.NewSeparator(),
		info,
	))
	w.Resize(fyne.NewSize(400, 250))

	// Обработчик завершения (Ctrl+C) — сбрасывает системный прокси
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Завершение работы...")
		if isRunning {
			setSystemProxy(false)
			stopProxy()
		}
		os.Exit(0)
	}()

	w.ShowAndRun()
}

func updateStatsLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		<-ticker.C
		if !isRunning {
			return
		}
		mu.Lock()
		txt := fmt.Sprintf("Запросов: %d | Из кэша: %d", reqCount, cacheHits)
		mu.Unlock()
		fyne.Do(func() {
			statsLabel.SetText(txt)
		})
	}
}
