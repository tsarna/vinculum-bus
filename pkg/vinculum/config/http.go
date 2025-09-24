package config

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
	ctyjson "github.com/zclconf/go-cty/cty/json"
	"go.uber.org/zap"
)

type HttpServer struct {
	Name     string
	DefRange hcl.Range
	Server   *http.Server
}

type HttpServerDefinition struct {
	Listen      string                  `hcl:"listen"`
	DefRange    hcl.Range               `hcl:",def_range"`
	StaticFiles []staticFilesDefinition `hcl:"files,block"`
	Handlers    []handlerDefinition     `hcl:"handle,block"`
}

type staticFilesDefinition struct {
	UrlPath   string    `hcl:"urlpath,label"`
	Directory string    `hcl:"directory"`
	Disabled  bool      `hcl:"disabled,optional"`
	DefRange  hcl.Range `hcl:",def_range"`
}

type handlerDefinition struct {
	Route    string         `hcl:"route,label"`
	Action   hcl.Expression `hcl:"action"`
	Disabled bool           `hcl:"disabled,optional"`
	DefRange hcl.Range      `hcl:",def_range"`
}

func ProcessHttpServerBlock(config *Config, block *hcl.Block, remainingBody hcl.Body) hcl.Diagnostics {
	serverDef := HttpServerDefinition{}
	diags := gohcl.DecodeBody(remainingBody, config.evalCtx, &serverDef)
	if diags.HasErrors() {
		return diags
	}

	httpServers, ok := config.Servers["http"]
	if !ok {
		httpServers = make(map[string]Server)
		config.Servers["http"] = httpServers
	}

	existing, ok := httpServers[block.Labels[1]]
	if ok {
		return hcl.Diagnostics{
			&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Http server already defined",
				Detail:   fmt.Sprintf("Http server %s already defined at %s", block.Labels[1], existing.GetDefRange()),
				Subject:  &serverDef.DefRange,
			},
		}
	}

	mux := http.NewServeMux()

	for _, file := range serverDef.StaticFiles {
		if file.Disabled {
			continue
		}

		if strings.Contains(file.UrlPath, " ") {
			return hcl.Diagnostics{
				&hcl.Diagnostic{
					Severity: hcl.DiagError,
					Summary:  "Invalid URL path",
					Detail:   fmt.Sprintf("Invalid urlpath: %s", file.UrlPath),
					Subject:  &file.DefRange,
				},
			}
		}

		path := strings.TrimSuffix(file.UrlPath, "/") + "/"

		mux.Handle(path, NewLoggingMiddleware(config.Logger,
			http.StripPrefix(path, http.FileServer(http.Dir(file.Directory)))))
	}

	for _, handler := range serverDef.Handlers {
		if handler.Disabled {
			continue
		}

		mux.Handle(handler.Route, NewLoggingMiddleware(config.Logger, &httpAction{config: config, actionExpr: handler.Action}))
	}

	server := &HttpServer{
		Name:     block.Labels[1],
		DefRange: serverDef.DefRange,
		Server: &http.Server{
			Addr:    serverDef.Listen,
			Handler: mux,
		},
	}

	config.Servers["http"][block.Labels[1]] = server
	config.Startables = append(config.Startables, server)

	return nil
}

func (h *HttpServer) GetName() string {
	return h.Name
}

func (h *HttpServer) GetDefRange() hcl.Range {
	return h.DefRange
}

func (h *HttpServer) Start() error {
	return h.Server.ListenAndServe()
}

type loggingMiddleware struct {
	next   http.Handler
	logger *zap.Logger
}

func NewLoggingMiddleware(logger *zap.Logger, next http.Handler) http.Handler {
	return &loggingMiddleware{
		logger: logger,
		next:   next,
	}
}

func (l *loggingMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// TODO: Log status code
	l.logger.Info("Request", zap.String("method", r.Method), zap.String("url", r.URL.String()))
	l.next.ServeHTTP(w, r)
}

type httpAction struct {
	config     *Config
	actionExpr hcl.Expression
}

func (h *httpAction) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	evalCtx, diags := getHttpActionEvalContext(h.config, w, r)
	if diags.HasErrors() {
		h.config.Logger.Error("Error building evaluation context", zap.Error(diags))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	_, diags = h.actionExpr.Value(evalCtx)
	if diags.HasErrors() {
		h.config.Logger.Error("Error executing action", zap.Error(diags))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
}

func getHttpActionEvalContext(config *Config, w http.ResponseWriter, r *http.Request) (*hcl.EvalContext, hcl.Diagnostics) {
	builder := NewContext(r.Context()).
		WithStringAttribute("method", r.Method).
		WithStringAttribute("url", r.URL.String()).
		WithStringAttribute("proto", r.Proto).
		WithUInt64Attribute("proto_major", uint64(r.ProtoMajor)).
		WithUInt64Attribute("proto_minor", uint64(r.ProtoMinor)).
		WithStringAttribute("host", r.Host).
		WithStringAttribute("remote_addr", r.RemoteAddr).
		WithFunctions(getHttpContextFunctions(w, r))

	if user, password, ok := r.BasicAuth(); ok {
		builder = builder.WithStringAttribute("user", user).WithStringAttribute("password", password)
	}

	return builder.BuildEvalContext(config.evalCtx)
}

func getHttpContextFunctions(w http.ResponseWriter, r *http.Request) map[string]function.Function {
	return map[string]function.Function{
		"getbody":          getBodyFunc(w, r),
		"getformvalue":     makeGetFormValueFunc(w, r),
		"getcookie":        makeGetCookieFunc(w, r),
		"getheader":        makeGetHeaderFunc(w, r),
		"getpathvalue":     makeGetPathValueFunc(w, r),
		"getpostformvalue": makeGetPostFormValueFunc(w, r),
		"redirect":         makeRedirectFunc(w, r),
		"respond":          makeRespondFunc(w, r),
		"setheader":        makeSetHeaderFunc(w, r),
	}
}

func getBodyFunc(_ http.ResponseWriter, r *http.Request) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{},
		Type:   function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				return cty.StringVal(""), err
			}
			return cty.StringVal(string(body)), nil
		},
	})
}

func makeGetCookieFunc(_ http.ResponseWriter, r *http.Request) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{
			{Name: "key", Type: cty.String},
		},
		Type: function.StaticReturnType(cty.DynamicPseudoType),
		Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
			cookie, err := r.Cookie(args[0].AsString())
			if err != nil {
				return cty.NullVal(retType), err
			}
			return convertCookie(cookie), nil
		},
	})
}

func convertCookie(cookie *http.Cookie) cty.Value {
	if cookie == nil {
		return cty.NullVal(cty.Object(map[string]cty.Type{}))
	}

	// Convert SameSite enum to string
	var sameSiteStr string
	switch cookie.SameSite {
	case http.SameSiteDefaultMode:
		sameSiteStr = "Default"
	case http.SameSiteLaxMode:
		sameSiteStr = "Lax"
	case http.SameSiteStrictMode:
		sameSiteStr = "Strict"
	case http.SameSiteNoneMode:
		sameSiteStr = "None"
	default:
		sameSiteStr = "Default"
	}

	// Convert Expires time to string (RFC3339 format if not zero, empty if zero)
	var expiresStr string
	if !cookie.Expires.IsZero() {
		expiresStr = cookie.Expires.Format(time.RFC3339)
	}

	return cty.ObjectVal(map[string]cty.Value{
		"name":        cty.StringVal(cookie.Name),
		"value":       cty.StringVal(cookie.Value),
		"quoted":      cty.BoolVal(cookie.Quoted),
		"path":        cty.StringVal(cookie.Path),
		"domain":      cty.StringVal(cookie.Domain),
		"expires":     cty.StringVal(expiresStr),
		"rawExpires":  cty.StringVal(cookie.RawExpires),
		"maxAge":      cty.NumberIntVal(int64(cookie.MaxAge)),
		"secure":      cty.BoolVal(cookie.Secure),
		"httpOnly":    cty.BoolVal(cookie.HttpOnly),
		"sameSite":    cty.StringVal(sameSiteStr),
		"partitioned": cty.BoolVal(cookie.Partitioned),
		"raw":         cty.StringVal(cookie.Raw),
	})
}

func makeGetFormValueFunc(_ http.ResponseWriter, r *http.Request) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{
			{Name: "key", Type: cty.String},
		},
		Type: function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
			value := r.FormValue(args[0].AsString())
			return cty.StringVal(value), nil
		},
	})
}

func makeGetPostFormValueFunc(_ http.ResponseWriter, r *http.Request) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{
			{Name: "key", Type: cty.String},
		},
		Type: function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
			value := r.PostFormValue(args[0].AsString())
			return cty.StringVal(value), nil
		},
	})
}

func makeGetHeaderFunc(w http.ResponseWriter, r *http.Request) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{
			{Name: "key", Type: cty.String},
		},
		Type: function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
			value := r.Header.Get(args[0].AsString())
			return cty.StringVal(value), nil
		},
	})
}

func makeGetPathValueFunc(_ http.ResponseWriter, r *http.Request) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{
			{Name: "key", Type: cty.String},
		},
		Type: function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
			value := r.PathValue(args[0].AsString())
			return cty.StringVal(value), nil
		},
	})
}

func makeRedirectFunc(w http.ResponseWriter, r *http.Request) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{
			{Name: "code", Type: cty.Number},
			{Name: "url", Type: cty.String},
		},
		Type: function.StaticReturnType(cty.Bool),
		Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
			code, _ := args[0].AsBigFloat().Int64()
			// No-op: just return true without logging
			http.Redirect(w, r, args[1].AsString(), int(code))
			return cty.True, nil
		},
	})
}

func makeRespondFunc(w http.ResponseWriter, _ *http.Request) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{
			{Name: "code", Type: cty.Number},
			{Name: "body", Type: cty.DynamicPseudoType},
		},
		Type: function.StaticReturnType(cty.Bool),
		Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
			code, _ := args[0].AsBigFloat().Int64()
			// No-op: just return true without logging
			w.WriteHeader(int(code))
			if args[1].Type() == cty.String {
				w.Write([]byte(args[1].AsString()))
			} else {
				w.Header().Set("Content-Type", "application/json")
				body, err := ctyjson.Marshal(args[1], args[1].Type())
				if err != nil {
					return cty.False, fmt.Errorf("failed to marshal body: %w", err)
				}
				w.Write(body)
			}
			return cty.True, nil
		},
	})
}

func makeSetHeaderFunc(w http.ResponseWriter, _ *http.Request) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{
			{Name: "key", Type: cty.String},
			{Name: "value", Type: cty.String},
		},
		Type: function.StaticReturnType(cty.Bool),
		Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
			// No-op: just return true without logging
			w.Header().Set(args[0].AsString(), args[1].AsString())

			return cty.True, nil
		},
	})
}

func getStatusCodeObject() cty.Value {
	codes := make(map[string]cty.Value)
	for name, code := range statusCodes {
		codes[name] = cty.NumberIntVal(int64(code))
	}
	codes["by_code"] = getStatusCodeMap()
	return cty.ObjectVal(codes)
}

func getStatusCodeMap() cty.Value {
	codes := make(map[string]cty.Value)
	for name, code := range statusCodes {
		codes[strconv.Itoa(code)] = cty.StringVal(name)
	}
	return cty.MapVal(codes)
}

var statusCodes = map[string]int{
	"Continue":           http.StatusContinue,
	"SwitchingProtocols": http.StatusSwitchingProtocols,
	"Processing":         http.StatusProcessing,
	"EarlyHints":         http.StatusEarlyHints,

	"OK":                   http.StatusOK,
	"Created":              http.StatusCreated,
	"Accepted":             http.StatusAccepted,
	"NonAuthoritativeInfo": http.StatusNonAuthoritativeInfo,
	"NoContent":            http.StatusNoContent,
	"ResetContent":         http.StatusResetContent,
	"PartialContent":       http.StatusPartialContent,
	"MultiStatus":          http.StatusMultiStatus,
	"AlreadyReported":      http.StatusAlreadyReported,
	"IMUsed":               http.StatusIMUsed,

	"MultipleChoices":  http.StatusMultipleChoices,
	"MovedPermanently": http.StatusMovedPermanently,
	"Found":            http.StatusFound,
	"SeeOther":         http.StatusSeeOther,
	"NotModified":      http.StatusNotModified,
	"UseProxy":         http.StatusUseProxy,

	"TemporaryRedirect": http.StatusTemporaryRedirect,
	"PermanentRedirect": http.StatusPermanentRedirect,

	"BadRequest":                   http.StatusBadRequest,
	"Unauthorized":                 http.StatusUnauthorized,
	"PaymentRequired":              http.StatusPaymentRequired,
	"Forbidden":                    http.StatusForbidden,
	"NotFound":                     http.StatusNotFound,
	"MethodNotAllowed":             http.StatusMethodNotAllowed,
	"NotAcceptable":                http.StatusNotAcceptable,
	"ProxyAuthRequired":            http.StatusProxyAuthRequired,
	"RequestTimeout":               http.StatusRequestTimeout,
	"Conflict":                     http.StatusConflict,
	"Gone":                         http.StatusGone,
	"LengthRequired":               http.StatusLengthRequired,
	"PreconditionFailed":           http.StatusPreconditionFailed,
	"RequestEntityTooLarge":        http.StatusRequestEntityTooLarge,
	"RequestURITooLong":            http.StatusRequestURITooLong,
	"UnsupportedMediaType":         http.StatusUnsupportedMediaType,
	"RequestedRangeNotSatisfiable": http.StatusRequestedRangeNotSatisfiable,
	"ExpectationFailed":            http.StatusExpectationFailed,
	"Teapot":                       http.StatusTeapot,
	"MisdirectedRequest":           http.StatusMisdirectedRequest,
	"UnprocessableEntity":          http.StatusUnprocessableEntity,
	"Locked":                       http.StatusLocked,
	"FailedDependency":             http.StatusFailedDependency,
	"TooEarly":                     http.StatusTooEarly,
	"UpgradeRequired":              http.StatusUpgradeRequired,
	"PreconditionRequired":         http.StatusPreconditionRequired,
	"TooManyRequests":              http.StatusTooManyRequests,
	"RequestHeaderFieldsTooLarge":  http.StatusRequestHeaderFieldsTooLarge,
	"UnavailableForLegalReasons":   http.StatusUnavailableForLegalReasons,

	"InternalServerError":           http.StatusInternalServerError,
	"NotImplemented":                http.StatusNotImplemented,
	"BadGateway":                    http.StatusBadGateway,
	"ServiceUnavailable":            http.StatusServiceUnavailable,
	"GatewayTimeout":                http.StatusGatewayTimeout,
	"HTTPVersionNotSupported":       http.StatusHTTPVersionNotSupported,
	"VariantAlsoNegotiates":         http.StatusVariantAlsoNegotiates,
	"InsufficientStorage":           http.StatusInsufficientStorage,
	"LoopDetected":                  http.StatusLoopDetected,
	"NotExtended":                   http.StatusNotExtended,
	"NetworkAuthenticationRequired": http.StatusNetworkAuthenticationRequired,
}
