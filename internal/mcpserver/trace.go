package mcpserver

// Трассировка: traceparent из _meta продолжает трассу клиента и уходит в
// каждый исходящий вызов; свои спаны пишутся всегда, но уходят на OTLP только
// когда задан OTEL_EXPORTER_OTLP_ENDPOINT. Без переменной сети нет вовсе.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const (
	// metaKeyTraceparent — W3C Trace Context в _meta запроса (SEP-414).
	metaKeyTraceparent = "traceparent"
	// methodToolsCall — единственный метод, чей вызов образует спан: ресурсы и
	// промпты прокси отдаёт мета-инструментами, они приходят сюда же.
	methodToolsCall = "tools/call"

	toolCallSpan = "mcp.tool.call"
	connectSpan  = "mcp.server.connect"
	reloadSpan   = "mcp.reload"

	tracerName = "github.com/akrhin/mcprig"
)

// traceparentRE — форма W3C без разбора версии: версия, нули и ff проверяются
// отдельно, чтобы отказ называл причину в тесте.
var traceparentRE = regexp.MustCompile(`^[0-9a-f]{2}-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$`)

// spanContextOf разбирает traceparent. Битый отбрасывается молча: это не
// ошибка клиента, вызов идёт дальше без трассы.
func spanContextOf(tp string) (trace.SpanContext, bool) {
	if !traceparentRE.MatchString(tp) {
		return trace.SpanContext{}, false
	}
	// Версия 00 — единственная, что переносится без оговорок; ff во флагах
	// запрещён самим W3C.
	if tp[0:2] != "00" || tp[53:55] == "ff" {
		return trace.SpanContext{}, false
	}
	var (
		tid  trace.TraceID
		sid  trace.SpanID
		flag uint64
	)
	_, _ = hex.Decode(tid[:], []byte(tp[3:35]))
	_, _ = hex.Decode(sid[:], []byte(tp[36:52]))
	flag, err := strconv.ParseUint(tp[53:55], 16, 8)
	if err != nil || !tid.IsValid() || !sid.IsValid() {
		return trace.SpanContext{}, false
	}
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: trace.TraceFlags(flag),
		Remote:     true,
	}), true
}

// traceparentOf рендерит исходящий заголовок из спана этого запроса.
func traceparentOf(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ""
	}
	return "00-" + sc.TraceID().String() + "-" + sc.SpanID().String() + "-" + sc.TraceFlags().String()
}

// traceMeta — единственное, что из входящего _meta уходит вниз: остальное
// клиентское _meta чужим серверам не принадлежит.
func traceMeta(ctx context.Context) mcp.Meta {
	tp := traceparentOf(ctx)
	if tp == "" {
		return nil
	}
	return mcp.Meta{metaKeyTraceparent: tp}
}

// traceCalls оборачивает tools/call: traceparent клиента становится родителем
// спана вызова, и каждый исходящий вызов обработчика несёт наш span-id.
func traceCalls(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if method != methodToolsCall {
			return next(ctx, method, req)
		}
		call, _ := req.(*mcp.CallToolRequest)
		parent, traced := parentOf(call)
		if traced {
			ctx = trace.ContextWithRemoteSpanContext(ctx, parent)
		}
		ctx, span := otel.Tracer(tracerName).Start(ctx, toolCallSpan)
		defer span.End()
		if span.IsRecording() {
			span.SetAttributes(
				attribute.String("mcp.tool.name", toolNameOf(call)),
				attribute.String("mcp.trace.source", traceSource(traced)),
			)
		} else if traced {
			// Без экспортера спан не пишется, но исходящий вызов всё равно
			// несёт наш span-id: иначе трасса клиента обрывается на прокси.
			ctx = trace.ContextWithSpanContext(ctx, childOf(parent))
		}
		res, err := next(ctx, method, req)
		recordOutcome(span, res, err)
		return res, err
	}
}

// parentOf достаёт родителя из _meta запроса.
func parentOf(call *mcp.CallToolRequest) (trace.SpanContext, bool) {
	if call == nil || call.Params == nil {
		return trace.SpanContext{}, false
	}
	tp, _ := call.Params.Meta[metaKeyTraceparent].(string)
	return spanContextOf(tp)
}

func toolNameOf(call *mcp.CallToolRequest) string {
	if call == nil || call.Params == nil {
		return ""
	}
	return call.Params.Name
}

// traceSource помечает, откуда взялась трасса: клиентская трасса — не
// доказательство для аудита, её продолжает кто угодно.
func traceSource(client bool) string {
	if client {
		return "client"
	}
	return "proxy"
}

// childOf сохраняет trace-id клиента и выдаёт свой span-id для одного вызова.
// Ошибка чтения случайных байт оставляет контекст прежним: трасса теряется,
// вызов — нет.
func childOf(parent trace.SpanContext) trace.SpanContext {
	var id trace.SpanID
	for i := 0; i < 3; i++ {
		if _, err := rand.Read(id[:]); err != nil {
			return parent
		}
		if id.IsValid() {
			return trace.NewSpanContext(trace.SpanContextConfig{
				TraceID:    parent.TraceID(),
				SpanID:     id,
				TraceFlags: parent.TraceFlags(),
			})
		}
	}
	return parent
}

// recordOutcome пишет исход вызова: статус — протокольная ошибка, outcome —
// чем кончился вызов инструмента.
func recordOutcome(span trace.Span, res mcp.Result, err error) {
	outcome := "ok"
	switch {
	case err != nil:
		outcome = "error"
		// Текст ошибки апстрима повторяет ключи и пути, которые тело ответа
		// маскирует (scrubSecrets); маскировки на этом уровне нет, поэтому в
		// спан уходит только тип ошибки — факт отказа без содержимого.
		span.SetStatus(codes.Error, fmt.Sprintf("%T", err))
	case res == nil:
		outcome = "error"
	default:
		if r, ok := res.(*mcp.CallToolResult); ok {
			if r.IsError {
				outcome = "error"
			} else if r.Meta["lazyMcpPolicy"] == "blocked" {
				outcome = "blocked"
			}
		}
	}
	if span.IsRecording() {
		span.SetAttributes(attribute.String("mcp.tool.outcome", outcome))
	}
}

// spanOf отдаёт записывающий спан запроса; nil — экспорта нет, атрибуты
// собирать незачем.
func spanOf(ctx context.Context) trace.Span {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return nil
	}
	return span
}

// SetupTracing включает OTLP/HTTP-экспорт. Без OTEL_EXPORTER_OTLP_ENDPOINT
// провайдер не ставится: спаны не пишутся и никуда не идут, а проброс
// traceparent работает на вручную выданных id.
func SetupTracing(ctx context.Context) (func(context.Context) error, error) {
	noop := func(context.Context) error { return nil }
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		return noop, nil
	}
	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return noop, err
	}
	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithAttributes(attribute.String("service.name", serviceName())),
	)
	if err != nil {
		// Ярлык ресурса — украшение: без него спаны всё равно полезны.
		res = resource.Default()
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

func serviceName() string {
	if v := os.Getenv("OTEL_SERVICE_NAME"); v != "" {
		return v
	}
	return "mcprig"
}
