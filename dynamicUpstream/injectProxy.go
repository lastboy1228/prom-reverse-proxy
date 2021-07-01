package dynamicUpstream

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

var (
	defaultProxy *httputil.ReverseProxy
	nhProxy      *httputil.ReverseProxy
	sdProxy      *httputil.ReverseProxy
)

const (
	queryParam    = "query"
	matchersParam = "match[]"
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
}

func NewRoutes() *Routes {
	mux := http.NewServeMux()
	routes := &Routes{
		mux: mux,
	}
	mux.Handle("/api/v1/query", customUpstream())
	mux.Handle("/api/v1/query_range", customUpstream())
	mux.Handle("/", defaultProxy)
	return routes
}

func customUpstream() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		values := req.URL.Query()
		exp, err := parser.ParseExpr(values.Get(queryParam))
		if err != nil {
			return
		}
		log.Printf("%v", exp.Type())
		r := &route{labelMatchers: make(map[string]*labels.Matcher)}
		////////////////////////////////////////////////////////
		// get labels concerned
		r.labelMatchers["hostip"] = nil
		r.parseNode(exp)
		r.getProxy().ServeHTTP(w, req)
	})
}

////////////////////////////////////////////////////////
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

func (r *route) parseNode(node parser.Node) error {
	switch n := node.(type) {
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
		// inject labelselector
		if vs, ok := n.VectorSelector.(*parser.VectorSelector); ok {
			r.matchLabels(vs.LabelMatchers)
		}

	case *parser.VectorSelector:
		r.matchLabels(n.LabelMatchers)

	default:
		panic(fmt.Errorf("parser.Walk: unhandled node type %T", n))
	}

	return nil
}

func (r *route) matchLabels(sources []*labels.Matcher) {
	for _, source := range sources {
		if _, ok := r.labelMatchers[source.Name]; ok {
			r.labelMatchers[source.Name] = source
		}
	}
}

func (r *Routes) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}
