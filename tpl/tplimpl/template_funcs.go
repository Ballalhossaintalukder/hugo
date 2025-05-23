// Copyright 2025 The Hugo Authors. All rights reserved.
//
// Portions Copyright The Go Authors.

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tplimpl

import (
	"context"
	"reflect"
	"strings"

	"github.com/gohugoio/hugo/common/hreflect"
	"github.com/gohugoio/hugo/common/maps"
	"github.com/gohugoio/hugo/identity"
	"github.com/gohugoio/hugo/tpl"

	texttemplate "github.com/gohugoio/hugo/tpl/internal/go_templates/texttemplate"
)

var (
	_    texttemplate.ExecHelper = (*templateExecHelper)(nil)
	zero reflect.Value
)

type templateExecHelper struct {
	watching   bool // whether we're in server/watch mode.
	site       reflect.Value
	siteParams reflect.Value
	funcs      map[string]reflect.Value
}

func (t *templateExecHelper) GetFunc(ctx context.Context, tmpl texttemplate.Preparer, name string) (fn reflect.Value, firstArg reflect.Value, found bool) {
	if fn, found := t.funcs[name]; found {
		if fn.Type().NumIn() > 0 {
			first := fn.Type().In(0)
			if hreflect.IsContextType(first) {
				// TODO(bep) check if we can void this conversion every time -- and if that matters.
				// The first argument may be context.Context. This is never provided by the end user, but it's used to pass down
				// contextual information, e.g. the top level data context (e.g. Page).
				return fn, reflect.ValueOf(ctx), true
			}
		}

		return fn, zero, true
	}
	return zero, zero, false
}

func (t *templateExecHelper) Init(ctx context.Context, tmpl texttemplate.Preparer) {
	if t.watching {
		_, ok := tmpl.(identity.IdentityProvider)
		if ok {
			t.trackDependencies(ctx, tmpl, "", reflect.Value{})
		}

	}
}

func (t *templateExecHelper) GetMapValue(ctx context.Context, tmpl texttemplate.Preparer, receiver, key reflect.Value) (reflect.Value, bool) {
	if params, ok := receiver.Interface().(maps.Params); ok {
		// Case insensitive.
		keystr := strings.ToLower(key.String())
		v, found := params[keystr]
		if !found {
			return zero, false
		}
		return reflect.ValueOf(v), true
	}

	v := receiver.MapIndex(key)

	return v, v.IsValid()
}

var typeParams = reflect.TypeOf(maps.Params{})

func (t *templateExecHelper) GetMethod(ctx context.Context, tmpl texttemplate.Preparer, receiver reflect.Value, name string) (method reflect.Value, firstArg reflect.Value) {
	if strings.EqualFold(name, "mainsections") && receiver.Type() == typeParams && receiver.Pointer() == t.siteParams.Pointer() {
		// Moved to site.MainSections in Hugo 0.112.0.
		receiver = t.site
		name = "MainSections"
	}

	if t.watching {
		ctx = t.trackDependencies(ctx, tmpl, name, receiver)
	}

	fn := hreflect.GetMethodByName(receiver, name)
	if !fn.IsValid() {
		return zero, zero
	}

	if fn.Type().NumIn() > 0 {
		first := fn.Type().In(0)
		if hreflect.IsContextType(first) {
			// The first argument may be context.Context. This is never provided by the end user, but it's used to pass down
			// contextual information, e.g. the top level data context (e.g. Page).
			return fn, reflect.ValueOf(ctx)
		}
	}

	return fn, zero
}

func (t *templateExecHelper) OnCalled(ctx context.Context, tmpl texttemplate.Preparer, name string, args []reflect.Value, result reflect.Value) {
	if !t.watching {
		return
	}

	// This switch is mostly for speed.
	switch name {
	case "Unmarshal":
	default:
		return
	}
	idm := tpl.Context.GetDependencyManagerInCurrentScope(ctx)
	if idm == nil {
		return
	}

	for _, arg := range args {
		identity.WalkIdentitiesShallow(arg.Interface(), func(level int, id identity.Identity) bool {
			idm.AddIdentity(id)
			return false
		})
	}
}

func (t *templateExecHelper) trackDependencies(ctx context.Context, tmpl texttemplate.Preparer, name string, receiver reflect.Value) context.Context {
	if tmpl == nil {
		panic("must provide a template")
	}

	idm := tpl.Context.GetDependencyManagerInCurrentScope(ctx)
	if idm == nil {
		return ctx
	}

	if info, ok := tmpl.(identity.IdentityProvider); ok {
		idm.AddIdentity(info.GetIdentity())
	}

	// The receive is the "." in the method execution or map lookup, e.g. the Page in .Resources.
	if hreflect.IsValid(receiver) {
		in := receiver.Interface()

		if idlp, ok := in.(identity.ForEeachIdentityByNameProvider); ok {
			// This will skip repeated .RelPermalink usage on transformed resources
			// which is not fingerprinted, e.g. to
			// prevent all HTML pages to be re-rendered on a small CSS change.
			idlp.ForEeachIdentityByName(name, func(id identity.Identity) bool {
				idm.AddIdentity(id)
				return false
			})
		} else {
			identity.WalkIdentitiesShallow(in, func(level int, id identity.Identity) bool {
				idm.AddIdentity(id)
				return false
			})
		}
	}

	return ctx
}
