package interpolate

import (
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const (
	OpHost uint32 = iota + 1
	OpPath
	OpQuery
	OpQueries
	OpMethod
	OpRawURI
	OpScheme
	OpPort
	OpHeader
	OpRemoteIP
)

var (
	tagRegex = regexp.MustCompile(`(?i)\{(host|path|queries|method|rawuri|scheme|port|remoteip|query\..*?|header\..*?)\}`)
)

func Analyze(origin string) []uint32 {
	ops := make([]uint32, 0)

	matches := tagRegex.FindAllStringSubmatchIndex(origin, -1)

	for _, m := range matches {
		start, end := m[0], m[1]
		tag := strings.ToLower(origin[m[2]:m[3]])
		var op uint32
		switch {
		case tag == "host":
			op = OpHost
		case tag == "path":
			op = OpPath
		case tag == "queries":
			op = OpQueries
		case tag == "method":
			op = OpMethod
		case tag == "rawuri":
			op = OpRawURI
		case tag == "scheme":
			op = OpScheme
		case tag == "port":
			op = OpPort
		case tag == "remoteip":
			op = OpRemoteIP
		case strings.HasPrefix(tag, "query."):
			op = OpQuery
		case strings.HasPrefix(tag, "header."):
			op = OpHeader
		default:
			continue
		}

		if op == 0 {
			continue
		}

		ops = append(ops, op, uint32(start), uint32(end))
	}

	return ops
}

type RequestContext struct {
	request      *http.Request
	queryParams  url.Values
	encodedQuery string
	remoteIP     string
}

func New(r *http.Request) *RequestContext {
	queries := r.URL.Query()
	remoteIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		remoteIP = r.RemoteAddr
	}
	return &RequestContext{
		request:      r,
		queryParams:  queries,
		encodedQuery: queries.Encode(),
		remoteIP:     remoteIP,
	}
}

func (r *RequestContext) Replace(origin string, metadata []uint32) string {
	if len(metadata) == 0 {
		return origin
	}

	var result strings.Builder

	result.Grow(len(origin) + 64)

	var lastPos uint32 = 0

	for i := 0; i < len(metadata); i += 3 {
		code, tagStart, tagEnd := metadata[i], metadata[i+1], metadata[i+2]

		result.WriteString(origin[lastPos:tagStart])
		lastPos = tagEnd

		switch code {
		case OpHost:
			result.WriteString(r.request.Host)
		case OpPath:
			result.WriteString(r.request.URL.Path)
		case OpQuery:
			result.WriteString(r.queryParams.Get(origin[tagStart+7 : tagEnd-1]))
		case OpQueries:
			result.WriteString(r.encodedQuery)
		case OpMethod:
			result.WriteString(r.request.Method)
		case OpRawURI:
			result.WriteString(r.request.RequestURI)
		case OpScheme:
			result.WriteString(r.request.URL.Scheme)
		case OpPort:
			result.WriteString(r.request.URL.Port())
		case OpRemoteIP:
			result.WriteString(r.remoteIP)
		case OpHeader:
			result.WriteString(r.request.Header.Get(origin[tagStart+8 : tagEnd-1]))
		}

	}

	result.WriteString(origin[lastPos:])

	return result.String()
}
