package connection

import (
    j "encoding/json"
    "bytes"
    "testing"
    "github.com/google/uuid"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "context"
    "crypto/rand"
    "fmt"
    "io"
    "math/big"
    "net/http"
    "time"

    pkgerrors "github.com/pkg/errors"
    "github.com/rs/zerolog"

    cfdflow "github.com/cloudflare/cloudflared/flow"

    "github.com/cloudflare/cloudflared/stream"
    "github.com/cloudflare/cloudflared/tracing"
    tunnelpogs "github.com/cloudflare/cloudflared/tunnelrpc/pogs"
    "github.com/cloudflare/cloudflared/websocket"
    "encoding/base64"
    "net"
    "bufio"
)

const (
    largeFileSize   = 2 * 1024 * 1024
    testGracePeriod = time.Millisecond * 100
)

var (
    testOrchestrator = &mockOrchestrator{
    originProxy: &mockOriginProxy{},
    }
    log           = zerolog.Nop()
    testLargeResp = make([]byte, largeFileSize)
)

var _ ReadWriteAcker = (*HTTPResponseReadWriteAcker)(nil)

type testRequest struct {
    name           string
    endpoint       string
    expectedStatus int
    expectedBody   []byte
    isProxyError   bool
}

type mockOrchestrator struct {
    originProxy OriginProxy
}

func (mcr *mockOrchestrator) GetConfigJSON() ([]byte, error) {
    return nil, fmt.Errorf("not implemented")
}

func (*mockOrchestrator) UpdateConfig(version int32, config []byte) *tunnelpogs.UpdateConfigurationResponse {
    return &tunnelpogs.UpdateConfigurationResponse{
    LastAppliedVersion: version,
    }
}

func (mcr *mockOrchestrator) GetOriginProxy() (OriginProxy, error) {
    return mcr.originProxy, nil
}

func (mcr *mockOrchestrator) WarpRoutingEnabled() (enabled bool) {
    return true
}

type mockOriginProxy struct{}

func (moc *mockOriginProxy) ProxyHTTP(
    w ResponseWriter,
    tr *tracing.TracedHTTPRequest,
    isWebsocket bool,
) error {
    req := tr.Request
    if isWebsocket {
    switch req.URL.Path {
    case "/ws/echo":
    return wsEchoEndpoint(w, req)
    case "/ws/flaky":
    return wsFlakyEndpoint(w, req)
    default:
    originRespEndpoint(w, http.StatusNotFound, []byte("ws endpoint not found"))
    return fmt.Errorf("unknown websocket endpoint %s", req.URL.Path)
    }
    }
    switch req.URL.Path {
    case "/ok":
    originRespEndpoint(w, http.StatusOK, []byte(http.StatusText(http.StatusOK)))
    case "/large_file":
    originRespEndpoint(w, http.StatusOK, testLargeResp)
    case "/400":
    originRespEndpoint(w, http.StatusBadRequest, []byte(http.StatusText(http.StatusBadRequest)))
    case "/500":
    originRespEndpoint(w, http.StatusInternalServerError, []byte(http.StatusText(http.StatusInternalServerError)))
    case "/error":
    return fmt.Errorf("Failed to proxy to origin")
    default:
    originRespEndpoint(w, http.StatusNotFound, []byte("page not found"))
    }
    return nil
}

func (moc *mockOriginProxy) ProxyTCP(
    ctx context.Context,
    rwa ReadWriteAcker,
    r *TCPRequest,
) error {
    if r.CfTraceID == "flow-rate-limited" {
    return pkgerrors.Wrap(cfdflow.ErrTooManyActiveFlows, "tcp flow rate limited")
    }

    return nil
}

type echoPipe struct {
    reader *io.PipeReader
    writer *io.PipeWriter
}

func (ep *echoPipe) Read(p []byte) (int, error) {
    return ep.reader.Read(p)
}

func (ep *echoPipe) Write(p []byte) (int, error) {
    return ep.writer.Write(p)
}

// A mock origin that echos data by streaming like a tcpOverWSConnection
// https://github.com/cloudflare/cloudflared/blob/master/ingress/origin_connection.go
func wsEchoEndpoint(w ResponseWriter, r *http.Request) error {
    resp := &http.Response{
    StatusCode: http.StatusSwitchingProtocols,
    }
    if err := w.WriteRespHeaders(resp.StatusCode, resp.Header); err != nil {
    return err
    }
    wsCtx, cancel := context.WithCancel(r.Context())
    readPipe, writePipe := io.Pipe()

    wsConn := websocket.NewConn(wsCtx, NewHTTPResponseReadWriterAcker(w, w.(http.Flusher), r), &log)
    go func() {
    select {
    case <-wsCtx.Done():
    case <-r.Context().Done():
    }
    readPipe.Close()
    writePipe.Close()
    }()

    originConn := &echoPipe{reader: readPipe, writer: writePipe}
    stream.Pipe(wsConn, originConn, &log)
    cancel()
    wsConn.Close()
    return nil
}

type flakyConn struct {
    closeAt time.Time
}

func (fc *flakyConn) Read(p []byte) (int, error) {
    if time.Now().After(fc.closeAt) {
    return 0, io.EOF
    }
    n := copy(p, "Read from flaky connection")
    return n, nil
}

func (fc *flakyConn) Write(p []byte) (int, error) {
    if time.Now().After(fc.closeAt) {
    return 0, fmt.Errorf("flaky connection closed")
    }
    return len(p), nil
}

func wsFlakyEndpoint(w ResponseWriter, r *http.Request) error {
    resp := &http.Response{
    StatusCode: http.StatusSwitchingProtocols,
    }
    if err := w.WriteRespHeaders(resp.StatusCode, resp.Header); err != nil {
    return err
    }
    wsCtx, cancel := context.WithCancel(r.Context())

    wsConn := websocket.NewConn(wsCtx, NewHTTPResponseReadWriterAcker(w, w.(http.Flusher), r), &log)

    rInt, _ := rand.Int(rand.Reader, big.NewInt(50))
    closedAfter := time.Millisecond * time.Duration(rInt.Int64())
    originConn := &flakyConn{closeAt: time.Now().Add(closedAfter)}
    stream.Pipe(wsConn, originConn, &log)
    cancel()
    wsConn.Close()
    return nil
}

func originRespEndpoint(w ResponseWriter, status int, data []byte) {
    resp := &http.Response{
    StatusCode: status,
    }
    _ = w.WriteRespHeaders(resp.StatusCode, resp.Header)
    _, _ = w.Write(data)
}

type mockConnectedFuse struct{}

func (mcf mockConnectedFuse) Connected() {}

func (mcf mockConnectedFuse) IsConnected() bool {
    return true
}


func TestCredentials(t *testing.T) {
    // Create test credentials
    tunnelID, err := uuid.Parse("88888888-4444-4444-4444-cccccccccccc")
    require.NoError(t, err)
    
    creds := Credentials{
    AccountTag:   "abcdef123456",
    TunnelSecret: []byte("secretsecretsecretsecret"),
    TunnelID:     tunnelID,
    }
    
    // Test Auth() method
    auth := creds.Auth()
    assert.Equal(t, "abcdef123456", auth.AccountTag)
    assert.Equal(t, []byte("secretsecretsecretsecret"), auth.TunnelSecret)
}

func TestTunnelToken(t *testing.T) {
    // Create test tunnel token
    tunnelID, err := uuid.Parse("88888888-4444-4444-4444-cccccccccccc")
    require.NoError(t, err)
    
    token := TunnelToken{
    AccountTag:   "abcdef123456",
    TunnelSecret: []byte("secretsecretsecretsecret"),
    TunnelID:     tunnelID,
    }
    
    // Test Credentials() method
    creds := token.Credentials()
    assert.Equal(t, "abcdef123456", creds.AccountTag)
    assert.Equal(t, []byte("secretsecretsecretsecret"), creds.TunnelSecret)
    assert.Equal(t, tunnelID, creds.TunnelID)
    
    // Test Encode() method
    encoded, err := token.Encode()
    require.NoError(t, err)
    assert.NotEmpty(t, encoded)
    
    // Decode and verify
    decoded, err := base64.StdEncoding.DecodeString(encoded)
    require.NoError(t, err)
    
    var decodedToken TunnelToken
    err = j.Unmarshal(decoded, &decodedToken)
    require.NoError(t, err)
    
    assert.Equal(t, token.AccountTag, decodedToken.AccountTag)
    assert.Equal(t, token.TunnelSecret, decodedToken.TunnelSecret)
    assert.Equal(t, token.TunnelID, decodedToken.TunnelID)
}

func TestConnectionType(t *testing.T) {
    // Test Type.shouldFlush()
    tests := []struct{
    connType Type
    expected bool
    }{
    {TypeWebsocket, true},
    {TypeTCP, true},
    {TypeControlStream, true},
    {TypeHTTP, false},
    {TypeConfiguration, false},
    {Type(99), false}, // Unknown type
    }
    
    for _, test := range tests {
    assert.Equal(t, test.expected, test.connType.shouldFlush(), 
    "shouldFlush() returned incorrect value for Type %s", test.connType.String())
    }
    
    // Test Type.String()
    assert.Equal(t, "websocket", TypeWebsocket.String())
    assert.Equal(t, "tcp", TypeTCP.String())
    assert.Equal(t, "control stream", TypeControlStream.String())
    assert.Equal(t, "http", TypeHTTP.String())
    assert.Equal(t, "Unknown Type 99", Type(99).String())
}

func TestLocalProxyConnection(t *testing.T) {
    // Create a mock ReadWriteCloser
    mockRWC := &mockReadWriteCloser{
    readData: []byte("test data"),
    }
    
    conn := &localProxyConnection{
    ReadWriteCloser: mockRWC,
    }
    
    // Test Read
    buf := make([]byte, 100)
    n, err := conn.Read(buf)
    assert.NoError(t, err)
    assert.Equal(t, 9, n)
    assert.Equal(t, "test data", string(buf[:n]))
    
    // Test Write
    n, err = conn.Write([]byte("response data"))
    assert.NoError(t, err)
    assert.Equal(t, 13, n)
    assert.Equal(t, "response data", string(mockRWC.writtenData))
    
    // Test Close
    err = conn.Close()
    assert.NoError(t, err)
    assert.True(t, mockRWC.closed)
    
    // Test other methods (that do nothing but need to be called for coverage)
    addr := conn.LocalAddr()
    assert.NotNil(t, addr)
    
    addr = conn.RemoteAddr()
    assert.NotNil(t, addr)
    
    // These methods should return nil (they're no-ops)
    assert.NoError(t, conn.SetDeadline(time.Now()))
    assert.NoError(t, conn.SetReadDeadline(time.Now()))
    assert.NoError(t, conn.SetWriteDeadline(time.Now()))
}

func TestUtilityFunctions(t *testing.T) {
    // Test uint8ToString
    tests := []struct{
    input    uint8
    expected string
    }{
    {0, "0"},
    {1, "1"},
    {127, "127"},
    {255, "255"},
    }
    
    for _, test := range tests {
    assert.Equal(t, test.expected, uint8ToString(test.input))
    }
    
    // Test FindCfRayHeader
    req, err := http.NewRequest("GET", "/", nil)
    require.NoError(t, err)
    assert.Empty(t, FindCfRayHeader(req))
    
    req.Header.Set("Cf-Ray", "abc123")
    assert.Equal(t, "abc123", FindCfRayHeader(req))
    
    // Test IsLBProbeRequest
    req, err = http.NewRequest("GET", "/", nil)
    require.NoError(t, err)
    assert.False(t, IsLBProbeRequest(req))
    
    req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Cloudflare-Traffic-Manager/1.0; +https://www.cloudflare.com/traffic-manager/; probe)")
    assert.True(t, IsLBProbeRequest(req))
    
    // Test shouldFlush
    headers := http.Header{}
    assert.False(t, shouldFlush(headers))
    
    headers.Set("content-type", "text/html")
    assert.False(t, shouldFlush(headers))
    
    headers.Set("content-type", "text/event-stream")
    assert.True(t, shouldFlush(headers))
    
    headers.Set("content-type", "application/grpc")
    assert.True(t, shouldFlush(headers))
    
    // Test with mixed case
    headers.Set("content-type", "Text/Event-Stream")
    assert.True(t, shouldFlush(headers))
}

func TestHTTPResponseReadWriteAcker(t *testing.T) {
    mockResponse := &mockResponseWriter{}
    mockFlusher := &mockFlusher{}
    
    body := bytes.NewBufferString("request body")
    req, err := http.NewRequest("POST", "/test", body)
    require.NoError(t, err)
    
    acker := NewHTTPResponseReadWriterAcker(mockResponse, mockFlusher, req)
    
    // Test Read
    buf := make([]byte, 100)
    n, err := acker.Read(buf)
    assert.NoError(t, err)
    assert.Equal(t, "request body", string(buf[:n]))
    
    // Test Write
    n, err = acker.Write([]byte("response data"))
    assert.NoError(t, err)
    assert.Equal(t, "response data", string(mockResponse.written))
    assert.True(t, mockFlusher.flushed, "Flusher should be called after Write")
    
    // Test AckConnection
    err = acker.AckConnection("trace-id-123")
    assert.NoError(t, err)
    assert.Equal(t, http.StatusSwitchingProtocols, mockResponse.statusCode)
    assert.Contains(t, mockResponse.headers, tracing.CanonicalCloudflaredTracingHeader)
    assert.Equal(t, "trace-id-123", mockResponse.headers.Get(tracing.CanonicalCloudflaredTracingHeader))
}

// Mock implementations for testing
type mockReadWriteCloser struct {
    readData    []byte
    writtenData []byte
    closed      bool
    readErr     error
    writeErr    error
    closeErr    error
}

func (m *mockReadWriteCloser) Read(p []byte) (int, error) {
    if m.readErr != nil {
    return 0, m.readErr
    }
    return copy(p, m.readData), nil
}

func (m *mockReadWriteCloser) Write(p []byte) (int, error) {
    if m.writeErr != nil {
    return 0, m.writeErr
    }
    m.writtenData = make([]byte, len(p))
    copy(m.writtenData, p)
    return len(p), nil
}

func (m *mockReadWriteCloser) Close() error {
    m.closed = true
    return m.closeErr
}

type mockResponseWriter struct {
    written    []byte
    statusCode int
    headers    http.Header
    err        error
}

func (m *mockResponseWriter) Header() http.Header {
    if m.headers == nil {
    m.headers = http.Header{}
    }
    return m.headers
}

func (m *mockResponseWriter) Write(p []byte) (int, error) {
    if m.err != nil {
    return 0, m.err
    }
    m.written = make([]byte, len(p))
    copy(m.written, p)
    return len(p), nil
}

func (m *mockResponseWriter) WriteHeader(statusCode int) {
    m.statusCode = statusCode
}

func (m *mockResponseWriter) WriteRespHeaders(statusCode int, header http.Header) error {
    m.statusCode = statusCode
    if m.headers == nil {
    m.headers = http.Header{}
    }
    for k, v := range header {
    m.headers[k] = v
    }
    return m.err
}

func (m *mockResponseWriter) AddTrailer(trailerName, trailerValue string) {
    if m.headers == nil {
    m.headers = http.Header{}
    }
    m.headers.Add(trailerName, trailerValue)
}

func (m *mockResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
    // Not implemented for these tests
    return nil, nil, nil
}

type mockFlusher struct {
    flushed bool
}

func (m *mockFlusher) Flush() {
    m.flushed = true
}
