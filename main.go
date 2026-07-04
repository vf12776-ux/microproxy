package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
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
	cacheDir   = "cache"
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

	proxy.OnRequest().DoFunc(func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		mu.Lock()
		reqCount++
		mu.Unlock()

		url := r.URL.String()
		cachePath := getCachePath(url)

		if data, err := os.ReadFile(cachePath); err == nil {
			mu.Lock()
			cacheHits++
			mu.Unlock()
			log.Printf("[HIT] %s", url)

			resp := &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(data)),
				Header:     make(http.Header),
			}
			resp.Header.Set("X-Cache", "HIT")
			return r, resp
		}

		log.Printf("[FETCH] %s", url)
		return r, nil
	})

	proxy.OnResponse().DoFunc(func(r *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		if r == nil || r.Request == nil || r.StatusCode < 200 || r.StatusCode >= 300 {
			return r
		}
		body, err := io.ReadAll(r.Body)
		if err == nil {
			os.WriteFile(getCachePath(r.Request.URL.String()), body, 0644)
			log.Printf("[SAVE] %s (%d байт)", r.Request.URL.String(), len(body))
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		return r
	})

	server = &http.Server{Addr: ":8080", Handler: proxy}
	go server.ListenAndServe()
}

func stopProxy() {
	if server != nil {
		server.Close()
		server = nil
	}
}

func main() {
	a := app.New()
	w := a.NewWindow("MicroProxy - Кэширующий прокси")

	title := widget.NewLabelWithStyle("MicroProxy v0.1", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

	startBtn = widget.NewButton("Включить прокси", func() {
		if !isRunning {
			startProxy()
			isRunning = true
			startBtn.SetText("Выключить прокси")
			startBtn.Importance = widget.DangerImportance
			go updateStatsLoop()
		} else {
			stopProxy()
			isRunning = false
			startBtn.SetText("Включить прокси")
			startBtn.Importance = widget.SuccessImportance
			statsLabel.SetText("Прокси выключен")
		}
	})
	startBtn.Importance = widget.SuccessImportance

	statsLabel = widget.NewLabel("Прокси выключен")
	info := widget.NewLabel("Настрой браузер: прокси 127.0.0.1:8080")
	info.Wrapping = fyne.TextWrapWord

	w.SetContent(container.NewVBox(title, widget.NewSeparator(), startBtn, statsLabel, widget.NewSeparator(), info))
	w.Resize(fyne.NewSize(400, 250))
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

		// ПРАВИЛЬНОЕ обновление GUI через fyne.Do
		fyne.Do(func() {
			statsLabel.SetText(txt)
		})
	}
}
