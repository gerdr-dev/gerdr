package work

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/MoeForge/moejs"
	"github.com/MoeForge/moejs/engine"
)

// The bridge between ctx's host functions and moejs. moejs only calls engine.NativeFunc (realm, this, args); ctx has a
// hundred-odd members written as plain Go functions — func(id string, arg jsValue) map[string]any — so they read as
// what they do. fn wraps one by reflection: each argument converted to the parameter's type, the result to a JS value,
// and a panic of r.throw turned into a thrown Error.
//
// Argument conversion (undefined and null give the zero value, as a missing argument does):
//   - string / bool / int / int64 / float64: JavaScript's ToString / ToBoolean / ToNumber
//   - any, map[string]any, []any, []string: ToGo (integral numbers as int64, others as float64; functions as
//     *engine.Object, which toPlain drops); a value of the wrong shape throws a TypeError
//   - jsValue: as is; jsFunc: a function to call back
//
// Results: nothing → undefined; a nil interface → null; jsValue and *promise as they are; anything else through FromGo
// (maps and slices lazily, structs through their JSON).

type jsValue = moejs.Value

// jsFunc is a JS function a host function calls back (headers.forEach(fn)).
type jsFunc func(args ...jsValue) (jsValue, error)

// promise is what a host function returns for `await`: already settled (host calls finish before they return).
type promise struct{ v jsValue }

// jsThrow is r.throw's panic: fn recovers it and the call throws err.
type jsThrow struct{ err error }

var (
	tValue   = reflect.TypeFor[jsValue]()
	tFunc    = reflect.TypeFor[jsFunc]()
	tError   = reflect.TypeFor[error]()
	tPromise = reflect.TypeFor[*promise]()
)

// fn makes a Go function callable from JS (see above). Called while ctx is built, so a bad signature is a bug found
// by the first run of any test.
func (r *runner) fn(f any) moejs.NativeFunc {
	fv := reflect.ValueOf(f)
	ft := fv.Type()
	if ft.Kind() != reflect.Func || ft.IsVariadic() && ft.In(ft.NumIn()-1) != reflect.TypeFor[[]jsValue]() {
		panic(fmt.Sprintf("work: can't bind %T to JS", f))
	}
	return func(realm *moejs.Realm, this jsValue, args []jsValue) (res jsValue, err error) {
		defer func() {
			if x := recover(); x != nil {
				t, ok := x.(jsThrow)
				if !ok {
					panic(x)
				}
				res, err = moejs.Undefined(), t.err
			}
		}()
		var in []reflect.Value
		if ft.IsVariadic() {
			in = []reflect.Value{reflect.ValueOf(args)}
		} else {
			in = make([]reflect.Value, ft.NumIn())
			for i := range in {
				v, err := r.goArg(realm, moejs.Arg(args, i), ft.In(i))
				if err != nil {
					return moejs.Undefined(), err
				}
				in[i] = v
			}
		}
		var out []reflect.Value
		if ft.IsVariadic() {
			out = fv.CallSlice(in)
		} else {
			out = fv.Call(in)
		}
		if len(out) == 0 {
			return moejs.Undefined(), nil
		}
		return r.toJS(out[0].Interface())
	}
}

func (r *runner) goArg(realm *moejs.Realm, v jsValue, t reflect.Type) (reflect.Value, error) {
	if t == tValue {
		return reflect.ValueOf(v), nil
	}
	if v.IsNullish() {
		return reflect.Zero(t), nil
	}
	if t == tFunc {
		if !engine.IsCallable(v) {
			return reflect.Value{}, r.typeError("expected a function")
		}
		return reflect.ValueOf(jsFunc(func(args ...jsValue) (jsValue, error) {
			return r.rt.CallFunction(v, moejs.Undefined(), args...)
		})), nil
	}
	switch t.Kind() {
	case reflect.String:
		s, err := realm.ToString(v)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(s.GoString()).Convert(t), nil
	case reflect.Bool:
		return reflect.ValueOf(engine.ToBoolean(v)).Convert(t), nil
	case reflect.Int, reflect.Int64, reflect.Float64:
		n, err := realm.ToNumber(v)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(n).Convert(t), nil
	}
	g := r.toGo(v)
	switch t {
	case reflect.TypeFor[any]():
		if g == nil {
			return reflect.Zero(t), nil
		}
		return reflect.ValueOf(g), nil
	case reflect.TypeFor[map[string]any](), reflect.TypeFor[[]any]():
		if g == nil || reflect.TypeOf(g) != t {
			want := "an object"
			if t.Kind() == reflect.Slice {
				want = "an array"
			}
			return reflect.Value{}, r.typeError("expected %s", want)
		}
		return reflect.ValueOf(g), nil
	case reflect.TypeFor[[]string]():
		l, ok := g.([]any)
		if !ok {
			return reflect.Value{}, r.typeError("expected an array of strings")
		}
		out := make([]string, len(l))
		for i, x := range l {
			if s, ok := x.(string); ok {
				out[i] = s
			} else {
				out[i] = fmt.Sprint(x)
			}
		}
		return reflect.ValueOf(out), nil
	}
	panic(fmt.Sprintf("work: no JS conversion to %v", t))
}

// toJS turns what a host function returns into a JS value.
func (r *runner) toJS(v any) (jsValue, error) {
	switch x := v.(type) {
	case nil:
		return moejs.Null(), nil
	case jsValue:
		return x, nil
	case *promise:
		if x == nil {
			return moejs.Null(), nil
		}
		return x.v, nil
	case moejs.NativeFunc:
		return r.rt.FromGo(x)
	}
	if rv := reflect.ValueOf(v); (rv.Kind() == reflect.Map || rv.Kind() == reflect.Slice || rv.Kind() == reflect.Pointer) && rv.IsNil() {
		return moejs.Null(), nil
	}
	return r.rt.FromGo(v)
}

// toGo is ToGo that can't fail (a value that can't be read in Go is nil).
func (r *runner) toGo(v jsValue) any {
	g, err := r.rt.ToGo(v)
	if err != nil {
		return nil
	}
	return g
}

// obj makes a JS object of host functions: every Go function in m (nested maps too) bound with fn, the rest as values.
func (r *runner) obj(m map[string]any) jsValue {
	out := make(map[string]any, len(m))
	for k, v := range m {
		switch x := v.(type) {
		case map[string]any:
			out[k] = r.obj(x)
		case jsValue, moejs.NativeFunc:
			out[k] = x
		default:
			if v != nil && reflect.TypeOf(v).Kind() == reflect.Func {
				out[k] = r.fn(v)
			} else {
				out[k] = v
			}
		}
	}
	o, err := r.rt.FromGo(out)
	if err != nil {
		panic(err) // only host-built maps get here
	}
	return o
}

// throw makes the host function being called throw err (as an Error with err's message).
func (r *runner) throw(err error) { panic(jsThrow{err}) }

// typeError is a TypeError to throw (return it from a host function, or r.throw it).
func (r *runner) typeError(format string, args ...any) error {
	realm := r.rt.Realm()
	return realm.Throw(engine.ObjectValue(realm.NewError(engine.KindTypeError, format, args...)))
}

// thrownBy is the JS value a host function's error becomes (to reject a promise with).
func (r *runner) thrownBy(err error) jsValue {
	if v, ok := r.rt.ThrownValue(err); ok {
		return v
	}
	return moejs.String(err.Error())
}

// rejected is a failed promise. Host calls that return a promise fail this way rather than throwing synchronously,
// so ctx.mcp(…).catch(…) catches it, as it would an async function's.
func (r *runner) rejected(err error) *promise {
	p, _, reject := r.rt.NewPromise()
	_ = reject(r.thrownBy(err))
	return &promise{p}
}

func (r *runner) resolved(v any) *promise {
	jv, err := r.toJS(v)
	if err != nil {
		return r.rejected(err)
	}
	p, resolve, _ := r.rt.NewPromise()
	_ = resolve(jv)
	return &promise{p}
}

// getterJS defines an enumerable, non-configurable getter (fetch's timing.total_ms and truncated, which change as
// the body is read).
var getterJS = mustScript("getter", `(function (o, k, g) { Object.defineProperty(o, k, { get: g, enumerable: true }) })`)

func (r *runner) defineGetter(o jsValue, name string, get any) {
	if r.getter == nil {
		v, err := r.rt.RunScript(getterJS)
		if err != nil {
			panic(err)
		}
		r.getter = &v
	}
	g, err := r.rt.FromGo(r.fn(get))
	if err == nil {
		_, err = r.rt.CallFunction(*r.getter, moejs.Undefined(), o, moejs.String(name), g)
	}
	if err != nil {
		r.throw(err)
	}
}

func mustScript(name, src string) *moejs.Script {
	s, err := moejs.CompileScript(name, src)
	if err != nil {
		panic(err)
	}
	return s
}

var errNotFunction = errors.New("not a function")

// jsString is String(x) for a value ToGo gave: what a command-line argument or an env value reads as.
func jsString(x any) string {
	switch v := x.(type) {
	case string:
		return v
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	case nil:
		return "null"
	case []any:
		parts := make([]string, len(v))
		for i, e := range v {
			if e != nil {
				parts[i] = jsString(e)
			}
		}
		return strings.Join(parts, ",")
	case map[string]any:
		return "[object Object]"
	}
	return fmt.Sprint(x)
}
