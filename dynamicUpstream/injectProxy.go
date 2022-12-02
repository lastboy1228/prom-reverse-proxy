package dynamicUpstream

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"reflect"
	"strings"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

var (
	defaultProxy *httputil.ReverseProxy
	nhProxy      *httputil.ReverseProxy
	sdProxy      *httputil.ReverseProxy
)

const (
	queryParam = "query"
	matchParam = "match[]"
)

func init() {
	upstream, err := url.Parse("http://10.0.0.1:39999/")

	if err != nil {
		log.Fatalf("Failed to build parse upstream URL: %v", err)
	}
	defaultProxy = httputil.NewSingleHostReverseProxy(upstream)

	upstream, _ = url.Parse("http://10.0.0.2:39999/")
	nhProxy = httputil.NewSingleHostReverseProxy(upstream)

	upstream, _ = url.Parse("http://10.0.0.3:39999/")
	sdProxy = httputil.NewSingleHostReverseProxy(upstream)
}

type Routes struct {
	mux *http.ServeMux
}

type route struct {
	labelMatchers map[string]*labels.Matcher
	request       *http.Request
}

func NewRoutes() *Routes {
	mux := http.NewServeMux()
	routes := &Routes{
		mux: mux,
	}
	mux.Handle("/api/v1/query", customUpstream())
	mux.Handle("/api/v1/query_range", customUpstream())
	mux.Handle("/api/v1/query_exemplars", customUpstream())
	mux.Handle("/api/v1/series", customUpstream())
	mux.Handle("/", defaultProxy)
	return routes
}

func customUpstream() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		log.Printf("[%v]%v", req.RemoteAddr, req.RequestURI)
		params := req.URL.Query()
		var useQuery bool = false
		var useMatch bool = false
		var expressions []string = params[matchParam]
		var query string = params.Get(queryParam)
		if len(query) > 0 {
			useQuery = true
		} else if len(expressions) > 0 {
			useMatch = true
		}
		if !useQuery && !useMatch {
			defaultProxy.ServeHTTP(w, req)
			return
		}
		if useQuery {
			expressions = append(expressions, query)
		}

		r := &route{labelMatchers: make(map[string]*labels.Matcher), request: req}
		////////////////////////////////////////////////////////
		// 用于判断路由的关键label
		r.labelMatchers["hostip"] = nil
		r.labelMatchers["job"] = nil
		r.labelMatchers["exporter"] = nil
		r.labelMatchers["zone"] = nil
		var matchParamCount int = 0
		for _, exp := range expressions {
			if len(exp) == 0 {
				continue
			}

			expParsed, err := parser.ParseExpr(exp)
			if err != nil {
				log.Printf("error exp %v : %v", exp, err)
				return
			}
			// parser.Call实例：irate(ClickHouseProfileEvents_RealTimeMicroseconds{cluster="wzp", zone="china"}[150s])
			// parser.MatrixSelector实例：ClickHouseProfileEvents_RealTimeMicroseconds{cluster="wzp", zone="us"}[150s]
			// parser.VectorSelector实例：ClickHouseProfileEvents_RealTimeMicroseconds{cluster="wzp", zone="jp"}
			log.Printf("原表达式%s，类型%v", exp, reflect.TypeOf(expParsed))
			r.parseNode(expParsed)
			paramValue := expParsed.String()
			log.Printf("新表达式%s", paramValue)
			// 重新设置request的参数
			if useQuery {
				params.Set(queryParam, paramValue)
			} else {
				if matchParamCount == 0 {
					params.Set(matchParam, paramValue)
				} else {
					// array参数，从第二个开始使用Add进行添加
					params.Add(matchParam, paramValue)
				}
				matchParamCount++
			}
		}
		proxy := r.getProxy()
		req.URL.RawQuery = params.Encode()
		proxy.ServeHTTP(w, req)
	})
}

// //////////////////////////////////////////////////////
// customer this func to dynamic proxy to upstream
func (r *route) getProxy() *httputil.ReverseProxy {
	label := r.labelMatchers["hostip"]
	if label == nil {
		return defaultProxy
	}
	if strings.HasPrefix(label.Value, "10.18.") {
		return nhProxy
	}
	if strings.HasPrefix(label.Value, "10.16.") {
		return sdProxy
	}
	return defaultProxy
}

// 解析label匹配条件，并移除对zone的匹配
func (r *route) parseNode(node parser.Node) error {
	switch n := node.(type) {
	// 把获取复合查询内部的MatrixSelector、VectorSelector
	case *parser.EvalStmt:
		if err := r.parseNode(n.Expr); err != nil {
			return err
		}

	case parser.Expressions:
		for _, e := range n {
			if err := r.parseNode(e); err != nil {
				return err
			}
		}

	case *parser.AggregateExpr:
		if err := r.parseNode(n.Expr); err != nil {
			return err
		}

	case *parser.BinaryExpr:
		if err := r.parseNode(n.LHS); err != nil {
			return err
		}

		if err := r.parseNode(n.RHS); err != nil {
			return err
		}

	case *parser.Call:
		if err := r.parseNode(n.Args); err != nil {
			return err
		}

	case *parser.SubqueryExpr:
		if err := r.parseNode(n.Expr); err != nil {
			return err
		}

	case *parser.ParenExpr:
		if err := r.parseNode(n.Expr); err != nil {
			return err
		}

	case *parser.UnaryExpr:
		if err := r.parseNode(n.Expr); err != nil {
			return err
		}

	case *parser.NumberLiteral, *parser.StringLiteral:
	// nothing to do

	case *parser.MatrixSelector:
		if vs, ok := n.VectorSelector.(*parser.VectorSelector); ok {
			r.matchLabels(vs)
		}

	case *parser.VectorSelector:
		r.matchLabels(n)

	default:
		panic(fmt.Errorf("parser.Walk: unhandled node type %T", n))
	}

	return nil
}

func (r *route) matchLabels(v *parser.VectorSelector) {
	index := -1
	for i, source := range v.LabelMatchers {
		if strings.Compare(source.Name, "zone") == 0 {
			index = i
		}
		if _, ok := r.labelMatchers[source.Name]; ok {
			r.labelMatchers[source.Name] = source
		}
	}
	if index >= 0 {
		// 移除zone的匹配条件
		v.LabelMatchers = append(v.LabelMatchers[:index], v.LabelMatchers[index+1:]...)
	}
}

func (r *Routes) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}
