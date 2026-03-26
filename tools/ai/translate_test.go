package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func deepLTestResponse(translated, sourceLang string) map[string]any {
	return map[string]any{
		"translations": []map[string]any{
			{"text": translated, "detected_source_language": sourceLang},
		},
	}
}

func TestTranslate_TranslatesText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify auth header
		if !strings.HasPrefix(r.Header.Get("Authorization"), "DeepL-Auth-Key") {
			t.Error("missing DeepL auth header")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(deepLTestResponse("Bonjour le monde", "EN"))
	}))
	t.Cleanup(srv.Close)

	tool := &TranslateTool{
		client:   srv.Client(),
		apiKey:   "test-key:fx",
		freeAPI:  false,
		deepLURL: srv.URL,
	}

	result, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"text":        "Hello world",
		"target_lang": "FR",
	}))
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var out translateOutput
	mustUnmarshal(t, result, &out)

	if out.TranslatedText != "Bonjour le monde" {
		t.Errorf("TranslatedText = %q, want %q", out.TranslatedText, "Bonjour le monde")
	}
	if out.TargetLang != "FR" {
		t.Errorf("TargetLang = %q, want %q", out.TargetLang, "FR")
	}
	if out.CharCount != len("Hello world") {
		t.Errorf("CharCount = %d, want %d", out.CharCount, len("Hello world"))
	}
}

func TestTranslate_UppercasesLangCodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["target_lang"] != "FR" {
			t.Errorf("target_lang = %v, want FR (uppercased)", body["target_lang"])
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(deepLTestResponse("Bonjour", "EN"))
	}))
	t.Cleanup(srv.Close)

	tool := &TranslateTool{client: srv.Client(), apiKey: "k", deepLURL: srv.URL}
	tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"text": "Hello", "target_lang": "fr", // lowercase input
	}))
}

func TestTranslate_InvalidKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	tool := &TranslateTool{client: srv.Client(), apiKey: "bad", deepLURL: srv.URL}
	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"text": "Hello", "target_lang": "FR",
	}))
	if err == nil {
		t.Error("should error for 403")
	}
}

func TestTranslate_MissingText(t *testing.T) {
	tool := Translate("key")
	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{"target_lang": "FR"}))
	if err == nil {
		t.Error("should error when text is missing")
	}
}

func TestTranslate_MissingTargetLang(t *testing.T) {
	tool := Translate("key")
	_, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{"text": "hello"}))
	if err == nil {
		t.Error("should error when target_lang is missing")
	}
}

func TestTranslate_FreeAPIKeyDetection(t *testing.T) {
	t1 := Translate("key123:fx")
	if !t1.freeAPI {
		t.Error("key ending in :fx should set freeAPI=true")
	}
	t2 := Translate("key123")
	if t2.freeAPI {
		t.Error("key not ending in :fx should set freeAPI=false")
	}
}

func TestTranslate_NameAndSchema(t *testing.T) {
	tool := Translate("key")
	if tool.Name() != "translate" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "translate")
	}
	if tool.Schema().Description == "" {
		t.Error("Schema.Description should not be empty")
	}
}
