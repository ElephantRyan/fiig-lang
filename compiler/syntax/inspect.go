package syntax

import "reflect"

// Inspect calls visit for every node reachable from root (a *File, a Decl,
// a Stmt or an Expr), parents before children, each once. It walks by
// reflection, so a node kind added later is reached without changing it;
// that suits tools that look for one kind of node across a whole file,
// such as `fiig fmt` finding every `or` (ADR 0142 §5). A node's pointers to
// other declarations, which the checker may fill in, are walked only once.
func Inspect(root any, visit func(node any)) {
	seen := map[uintptr]bool{}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			if seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			walk(v.Elem()) // a struct visits itself, addressed
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Struct:
			if v.CanAddr() {
				visit(v.Addr().Interface())
			}
			for i := 0; i < v.NumField(); i++ {
				if v.Type().Field(i).IsExported() {
					walk(v.Field(i))
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		case reflect.Map:
			for _, k := range v.MapKeys() {
				walk(v.MapIndex(k))
			}
		}
	}
	walk(reflect.ValueOf(root))
}
