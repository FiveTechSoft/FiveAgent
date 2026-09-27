package agent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

func TestLooksLikeCode(t *testing.T) {
	code := []string{
		"```go\nfunc main() {}\n```",
		"func Add(a, b int) int",
		"SELECT * FROM users WHERE id = 1", // select + from: two weak signals
		"git commit failed, why?",
		"traceback: ValueError at line 3",
		"hay un bug en el bucle", // bug + bucle: two weak signals
		"escribe una función en go",
		"escríbeme un programa que sume dos números",
		"hazme un script en bash",
		"no me compila el proyecto",
		"¿cómo centro un div?",
		"corrige mi html por favor",
		"devuélveme un json con los datos",
		"quiero aprender java",
		"mi api no responde",
		"tengo un error en el bucle",
	}
	for _, s := range code {
		if !looksLikeCode(s) {
			t.Errorf("looksLikeCode(%q) = false, want true", s)
		}
	}
	chat := []string{
		"hola, ¿qué tal?",
		"buenos días, ¿me cuentas un chiste?",
		"let me think about it",
		"gracias, funciona genial",
		"nos vemos mañana",
		// Word boundaries: substrings inside other words do not fire.
		"me gustan las bugambilias",
		"that's def cool",
		// Single weak signals stay on the chat model, however code-ish
		// they read: ambiguous words need two distinct matches.
		"¿me explicas esta función?",
		"explícame esta función matemática",
		"¿qué función cumple el botón rojo?",
		"¿cuál es el código postal de Sevilla?",
		"tengo un código de descuento",
		"hay un bug en la app de fotos",
		"¿me explicas qué es un algoritmo?",
		"import os",
		"¿qué hace este método?",
		"arréglame el bucle infinito",
		"error en la línea 3 del archivo",
		"¿qué es una variable global?",
		"nice class today",
		"please select a date for the meeting",
		"the import of goods rose",
		"compile a list of restaurants",
		// Punctuation alone is not a signal.
		"hola; ¿cómo estás?",
	}
	for _, s := range chat {
		if looksLikeCode(s) {
			t.Errorf("looksLikeCode(%q) = true, want false", s)
		}
	}
}

// TestHandleRoutesCoderModel checks the agent picks the coder client for
// code-heavy text and the main client for plain chat.
func TestHandleRoutesCoderModel(t *testing.T) {
	var mainCalls, coderCalls atomic.Int32
	handler := func(n *atomic.Int32) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			n.Add(1)
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"reply"}}]}`)
		}
	}
	mainSrv := httptest.NewServer(handler(&mainCalls))
	defer mainSrv.Close()
	coderSrv := httptest.NewServer(handler(&coderCalls))
	defer coderSrv.Close()

	cfg := &config.Config{Model: config.Model{BaseURL: mainSrv.URL, Name: "main"}}
	a := New(model.NewOpenAICompat(cfg.Model), &fakeStore{}, tools.NewRegistry(), SystemPrompt(cfg))
	a.WithCoder(model.NewOpenAICompat(config.Model{BaseURL: coderSrv.URL, Name: "coder"}))

	if _, err := a.Handle(context.Background(), "whatsapp", "u1", "hola, ¿qué tal?"); err != nil {
		t.Fatal(err)
	}
	if mainCalls.Load() != 1 || coderCalls.Load() != 0 {
		t.Fatalf("chat: main=%d coder=%d, want main=1 coder=0", mainCalls.Load(), coderCalls.Load())
	}

	if _, err := a.Handle(context.Background(), "whatsapp", "u1", "escribe una función en go"); err != nil {
		t.Fatal(err)
	}
	if coderCalls.Load() != 1 {
		t.Fatalf("code: coder=%d, want 1 (main=%d)", coderCalls.Load(), mainCalls.Load())
	}
}
