package lua

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/argoproj/gitops-engine/pkg/health"
	glob "github.com/bmatcuk/doublestar/v4"
	lua "github.com/yuin/gopher-lua"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	luajson "layeh.com/gopher-json"

	applicationpkg "github.com/argoproj/argo-cd/v3/pkg/apiclient/application"
	appv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/argoproj/argo-cd/v3/resource_customizations"
	argoglob "github.com/argoproj/argo-cd/v3/util/glob"
)

const (
	incorrectReturnType       = "expect %s output from Lua script, not %s"
	invalidHealthStatus       = "Lua returned an invalid health status"
	healthScriptFile          = "health.lua"
	actionScriptFile          = "action.lua"
	actionDiscoveryScriptFile = "discovery.lua"
)

// errScriptDoesNotExist is an error type for when a built-in script does not exist.
var errScriptDoesNotExist = errors.New("built-in script does not exist")

type ResourceHealthOverrides map[string]appv1.ResourceOverride

func (overrides ResourceHealthOverrides) GetResourceHealth(obj *unstructured.Unstructured) (*health.HealthStatus, error) {
	luaVM := VM{
		ResourceOverrides: overrides,
	}
	script, useOpenLibs, err := luaVM.GetHealthScript(obj)
	if err != nil {
		return nil, err
	}
	if script == "" {
		return nil, nil
	}
	// enable/disable the usage of lua standard library
	luaVM.UseOpenLibs = useOpenLibs
	result, err := luaVM.ExecuteHealthLua(obj, script)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// VM Defines a struct that implements the luaVM
type VM struct {
	ResourceOverrides map[string]appv1.ResourceOverride
	// UseOpenLibs flag to enable open libraries. Libraries are disabled by default while running, but enabled during testing to allow the use of print statements
	UseOpenLibs bool
}

func (vm VM) runLua(obj *unstructured.Unstructured, script string) (*lua.LState, error) {
	return vm.runLuaWithResourceActionParameters(obj, script, nil)
}

func (vm VM) runLuaWithResourceActionParameters(obj *unstructured.Unstructured, script string, resourceActionParameters []*applicationpkg.ResourceActionParameters) (*lua.LState, error) {
	l := lua.NewState(lua.Options{
		SkipOpenLibs: !vm.UseOpenLibs,
	})
	defer l.Close()
	// Opens table library to allow access to functions to manipulate tables
	for _, pair := range []struct {
		n string
		f lua.LGFunction
	}{
		{lua.LoadLibName, lua.OpenPackage},
		{lua.BaseLibName, lua.OpenBase},
		{lua.TabLibName, lua.OpenTable},
		// load our 'safe' version of the OS library
		{lua.OsLibName, OpenSafeOs},
	} {
		if err := l.CallByParam(lua.P{
			Fn:      l.NewFunction(pair.f),
			NRet:    0,
			Protect: true,
		}, lua.LString(pair.n)); err != nil {
			panic(err)
		}
	}
	// preload our 'safe' version of the OS library. Allows the 'local os = require("os")' to work
	l.PreloadModule(lua.OsLibName, SafeOsLoader)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	l.SetContext(ctx)

	// Inject action parameters as a hash table global variable
	actionParams := l.CreateTable(0, len(resourceActionParameters))
	for _, resourceActionParameter := range resourceActionParameters {
		value := decodeValue(l, resourceActionParameter.GetValue())
		actionParams.RawSetH(lua.LString(resourceActionParameter.GetName()), value)
	}
	l.SetGlobal("actionParams", actionParams) // Set the actionParams table as a global variable

	objectValue := decodeValue(l, obj.Object)
	l.SetGlobal("obj", objectValue)
	err := l.DoString(script)

	// Remove the default lua stack trace from execution errors since these
	// errors will make it back to the user
	var apiErr *lua.ApiError
	if errors.As(err, &apiErr) {
		if apiErr.Type == lua.ApiErrorRun {
			apiErr.StackTrace = ""
			err = apiErr
		}
	}

	return l, err
}

// ExecuteHealthLua runs the lua script to generate the health status of a resource
func (vm VM) ExecuteHealthLua(obj *unstructured.Unstructured, script string) (*health.HealthStatus, error) {
	l, err := vm.runLua(obj, script)
	if err != nil {
		return nil, err
	}
	returnValue := l.Get(-1)
	if returnValue.Type() == lua.LTTable {
		jsonBytes, err := luajson.Encode(returnValue)
		if err != nil {
			return nil, err
		}
		healthStatus := &health.HealthStatus{}
		err = json.Unmarshal(jsonBytes, healthStatus)
		if err != nil {
			// Validate if the error is caused by an empty object
			typeError := &json.UnmarshalTypeError{Value: "array", Type: reflect.TypeOf(healthStatus)}
			if errors.As(err, &typeError) {
				return &health.HealthStatus{}, nil
			}
			return nil, err
		}
		if !isValidHealthStatusCode(healthStatus.Status) {
			return &health.HealthStatus{
				Status:  health.HealthStatusUnknown,
				Message: invalidHealthStatus,
			}, nil
		}

		return healthStatus, nil
	} else if returnValue.Type() == lua.LTNil {
		return &health.HealthStatus{}, nil
	}
	return nil, fmt.Errorf(incorrectReturnType, "table", returnValue.Type().String())
}

// GetHealthScript attempts to read lua script from config and then filesystem for that resource. If none exists, return
// an empty string.
func (vm VM) GetHealthScript(obj *unstructured.Unstructured) (script string, useOpenLibs bool, err error) {
	// first, search the gvk as is in the ResourceOverrides
	key := GetConfigMapKey(obj.GroupVersionKind())

	if script, ok := vm.ResourceOverrides[key]; ok && script.HealthLua != "" {
		return script.HealthLua, script.UseOpenLibs, nil
	}

	// if not found as is, perhaps it matches a wildcard entry in the configmap
	getWildcardHealthOverride, useOpenLibs := getWildcardHealthOverrideLua(vm.ResourceOverrides, obj.GroupVersionKind())

	if getWildcardHealthOverride != "" {
		return getWildcardHealthOverride, useOpenLibs, nil
	}

	// if not found in the ResourceOverrides at all, search it as is in the built-in scripts
	// (as built-in scripts are files in folders, named after the GVK, currently there is no wildcard support for them)
	builtInScript, err := vm.getPredefinedLuaScripts(key, healthScriptFile)
	if err != nil {
		if errors.Is(err, errScriptDoesNotExist) {
			// Try to find a wildcard built-in health script
			builtInScript, err = getWildcardBuiltInHealthOverrideLua(key)
			if err != nil {
				return "", false, fmt.Errorf("error while fetching built-in health script: %w", err)
			}
			if builtInScript != "" {
				return builtInScript, true, nil
			}

			// It's okay if no built-in health script exists. Just return an empty string and let the caller handle it.
			return "", false, nil
		}
		return "", false, err
	}
	// standard libraries will be enabled for all built-in scripts
	return builtInScript, true, err
}

func (vm VM) ExecuteResourceAction(obj *unstructured.Unstructured, script string, resourceActionParameters []*applicationpkg.ResourceActionParameters) ([]ImpactedResource, error) {
	l, err := vm.runLuaWithResourceActionParameters(obj, script, resourceActionParameters)
	if err != nil {
		return nil, err
	}
	returnValue := l.Get(-1)
	if returnValue.Type() == lua.LTTable {
		jsonBytes, err := luajson.Encode(returnValue)
		if err != nil {
			return nil, err
		}

		var impactedResources []ImpactedResource

		jsonString := bytes.NewBuffer(jsonBytes).String()
		// nolint:staticcheck // Lua is fine to be capitalized.
		if len(jsonString) < 2 {
			return nil, errors.New("Lua output was not a valid json object or array")
		}
		// The output from Lua is either an object (old-style action output) or an array (new-style action output).
		// Check whether the string starts with an opening square bracket and ends with a closing square bracket,
		// avoiding programming by exception.
		if jsonString[0] == '[' && jsonString[len(jsonString)-1] == ']' {
			// The string represents a new-style action array output
			impactedResources, err = UnmarshalToImpactedResources(string(jsonBytes))
			if err != nil {
				return nil, err
			}
		} else {
			// The string represents an old-style action object output
			newObj, err := appv1.UnmarshalToUnstructured(string(jsonBytes))
			if err != nil {
				return nil, err
			}
			// Wrap the old-style action output with a single-member array.
			// The default definition of the old-style action is a "patch" one.
			impactedResources = append(impactedResources, ImpactedResource{newObj, PatchOperation})
		}

		for _, impactedResource := range impactedResources {
			// Cleaning the resource is only relevant to "patch"
			if impactedResource.K8SOperation == PatchOperation {
				impactedResource.UnstructuredObj.Object = cleanReturnedObj(impactedResource.UnstructuredObj.Object, obj.Object)
			}
		}
		return impactedResources, nil
	}
	return nil, fmt.Errorf(incorrectReturnType, "table", returnValue.Type().String())
}

// UnmarshalToImpactedResources unmarshals an ImpactedResource array representation in JSON to ImpactedResource array
func UnmarshalToImpactedResources(resources string) ([]ImpactedResource, error) {
	if resources == "" || resources == "null" {
		return nil, nil
	}

	var impactedResources []ImpactedResource
	err := json.Unmarshal([]byte(resources), &impactedResources)
	if err != nil {
		return nil, err
	}
	return impactedResources, nil
}

// cleanReturnedObj Lua cannot distinguish an empty table as an array or map, and the library we are using choose to
// decoded an empty table into an empty array. This function prevents the lua scripts from unintentionally changing an
// empty struct into empty arrays
func cleanReturnedObj(newObj, obj map[string]any) map[string]any {
	mapToReturn := newObj
	for key := range obj {
		if newValueInterface, ok := newObj[key]; ok {
			oldValueInterface, ok := obj[key]
			if !ok {
				continue
			}
			switch newValue := newValueInterface.(type) {
			case map[string]any:
				if oldValue, ok := oldValueInterface.(map[string]any); ok {
					convertedMap := cleanReturnedObj(newValue, oldValue)
					mapToReturn[key] = convertedMap
				}

			case []any:
				switch oldValue := oldValueInterface.(type) {
				case map[string]any:
					if len(newValue) == 0 {
						mapToReturn[key] = oldValue
					}
				case []any:
					newArray := cleanReturnedArray(newValue, oldValue)
					mapToReturn[key] = newArray
				}
			}
		}
	}
	return mapToReturn
}

// cleanReturnedArray allows Argo CD to recurse into nested arrays when checking for unintentional empty struct to
// empty array conversions.
func cleanReturnedArray(newObj, obj []any) []any {
	arrayToReturn := newObj
	for i := range newObj {
		switch newValue := newObj[i].(type) {
		case map[string]any:
			if oldValue, ok := obj[i].(map[string]any); ok {
				convertedMap := cleanReturnedObj(newValue, oldValue)
				arrayToReturn[i] = convertedMap
			}
		case []any:
			if oldValue, ok := obj[i].([]any); ok {
				convertedMap := cleanReturnedArray(newValue, oldValue)
				arrayToReturn[i] = convertedMap
			}
		}
	}
	return arrayToReturn
}

func (vm VM) ExecuteResourceActionDiscovery(obj *unstructured.Unstructured, scripts []string) ([]appv1.ResourceAction, error) {
	if len(scripts) == 0 {
		return nil, errors.New("no action discovery script provided")
	}
	availableActionsMap := make(map[string]appv1.ResourceAction)

	for _, script := range scripts {
		l, err := vm.runLua(obj, script)
		if err != nil {
			return nil, err
		}
		returnValue := l.Get(-1)
		if returnValue.Type() != lua.LTTable {
			return nil, fmt.Errorf(incorrectReturnType, "table", returnValue.Type().String())
		}
		jsonBytes, err := luajson.Encode(returnValue)
		if err != nil {
			return nil, fmt.Errorf("error in converting to lua table: %w", err)
		}
		if noAvailableActions(jsonBytes) {
			continue
		}
		actionsMap := make(map[string]any)
		err = json.Unmarshal(jsonBytes, &actionsMap)
		if err != nil {
			return nil, fmt.Errorf("error unmarshaling action table: %w", err)
		}
		for key, value := range actionsMap {
			resourceAction := appv1.ResourceAction{Name: key, Disabled: isActionDisabled(value)}
			if _, exist := availableActionsMap[key]; exist {
				continue
			}
			if emptyResourceActionFromLua(value) {
				availableActionsMap[key] = resourceAction
				continue
			}
			resourceActionBytes, err := json.Marshal(value)
			if err != nil {
				return nil, fmt.Errorf("error marshaling resource action: %w", err)
			}

			err = json.Unmarshal(resourceActionBytes, &resourceAction)
			if err != nil {
				return nil, fmt.Errorf("error unmarshaling resource action: %w", err)
			}
			availableActionsMap[key] = resourceAction
		}
	}

	availableActions := make([]appv1.ResourceAction, 0, len(availableActionsMap))
	for _, action := range availableActionsMap {
		availableActions = append(availableActions, action)
	}

	return availableActions, nil
}

// Actions are enabled by default
func isActionDisabled(actionsMap any) bool {
	actions, ok := actionsMap.(map[string]any)
	if !ok {
		return false
	}
	for key, val := range actions {
		if vv, ok := val.(bool); ok {
			if key == "disabled" {
				return vv
			}
		}
	}
	return false
}

func emptyResourceActionFromLua(i any) bool {
	_, ok := i.([]any)
	return ok
}

func noAvailableActions(jsonBytes []byte) bool {
	// When the Lua script returns an empty table, it is decoded as a empty array.
	return string(jsonBytes) == "[]"
}

func (vm VM) GetResourceActionDiscovery(obj *unstructured.Unstructured) ([]string, error) {
	key := GetConfigMapKey(obj.GroupVersionKind())
	var discoveryScripts []string

	// Check if there are resource overrides for the given key
	override, ok := vm.ResourceOverrides[key]
	if ok && override.Actions != "" {
		actions, err := override.GetActions()
		if err != nil {
			return nil, err
		}
		// Append the action discovery Lua script if built-in actions are to be included
		if !actions.MergeBuiltinActions {
			return []string{actions.ActionDiscoveryLua}, nil
		}
		discoveryScripts = append(discoveryScripts, actions.ActionDiscoveryLua)
	}

	// Fetch predefined Lua scripts
	discoveryKey := key + "/actions/"
	discoveryScript, err := vm.getPredefinedLuaScripts(discoveryKey, actionDiscoveryScriptFile)
	if err != nil {
		if errors.Is(err, errScriptDoesNotExist) {
			// No worries, just return what we have.
			return discoveryScripts, nil
		}
		return nil, fmt.Errorf("error while fetching predefined lua scripts: %w", err)
	}

	discoveryScripts = append(discoveryScripts, discoveryScript)

	return discoveryScripts, nil
}

// GetResourceAction attempts to read lua script from config and then filesystem for that resource
func (vm VM) GetResourceAction(obj *unstructured.Unstructured, actionName string) (appv1.ResourceActionDefinition, error) {
	key := GetConfigMapKey(obj.GroupVersionKind())
	override, ok := vm.ResourceOverrides[key]
	if ok && override.Actions != "" {
		actions, err := override.GetActions()
		if err != nil {
			return appv1.ResourceActionDefinition{}, err
		}
		for _, action := range actions.Definitions {
			if action.Name == actionName {
				return action, nil
			}
		}
	}

	actionKey := fmt.Sprintf("%s/actions/%s", key, actionName)
	actionScript, err := vm.getPredefinedLuaScripts(actionKey, actionScriptFile)
	if err != nil {
		return appv1.ResourceActionDefinition{}, err
	}

	return appv1.ResourceActionDefinition{
		Name:      actionName,
		ActionLua: actionScript,
	}, nil
}

func GetConfigMapKey(gvk schema.GroupVersionKind) string {
	if gvk.Group == "" {
		return gvk.Kind
	}
	return fmt.Sprintf("%s/%s", gvk.Group, gvk.Kind)
}

// getWildcardHealthOverrideLua returns the first encountered resource override which matches the wildcard and has a
// non-empty health script. Having multiple wildcards with non-empty health checks that can match the GVK is
// non-deterministic.
func getWildcardHealthOverrideLua(overrides map[string]appv1.ResourceOverride, gvk schema.GroupVersionKind) (string, bool) {
	gvkKeyToMatch := GetConfigMapKey(gvk)

	for key, override := range overrides {
		if argoglob.Match(key, gvkKeyToMatch) && override.HealthLua != "" {
			return override.HealthLua, override.UseOpenLibs
		}
	}
	return "", false
}

func (vm VM) getPredefinedLuaScripts(objKey string, scriptFile string) (string, error) {
	data, err := resource_customizations.Embedded.ReadFile(filepath.Join(objKey, scriptFile))
	if err != nil {
		if os.IsNotExist(err) {
			return "", errScriptDoesNotExist
		}
		return "", err
	}
	return string(data), nil
}

// globHealthScriptPathsOnce is a sync.Once instance to ensure that the globHealthScriptPaths are only initialized once.
// The globs come from an embedded filesystem, so it won't change at runtime.
var globHealthScriptPathsOnce sync.Once

// globHealthScriptPaths is a cache for the glob patterns of directories containing health.lua files. Don't use this
// directly, use getGlobHealthScriptPaths() instead.
var globHealthScriptPaths []string

// getGlobHealthScriptPaths returns the paths of the directories containing health.lua files where the path contains a
// glob pattern. It uses a sync.Once to ensure that the paths are only initialized once.
func getGlobHealthScriptPaths() ([]string, error) {
	var err error
	globHealthScriptPathsOnce.Do(func() {
		// Walk through the embedded filesystem and get the directory names of all directories containing a health.lua.
		var patterns []string
		err = fs.WalkDir(resource_customizations.Embedded, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return fmt.Errorf("error walking path %q: %w", path, err)
			}

			// Skip non-directories at the top level
			if d.IsDir() && filepath.Dir(path) == "." {
				return nil
			}

			// Check if the directory contains a health.lua file
			if filepath.Base(path) != healthScriptFile {
				return nil
			}

			groupKindPath := filepath.Dir(path)
			// Check if the path contains a wildcard. If it doesn't, skip it.
			if !strings.Contains(groupKindPath, "_") {
				return nil
			}

			pattern := strings.ReplaceAll(groupKindPath, "_", "*")
			// Check that the pattern is valid.
			if !glob.ValidatePattern(pattern) {
				return fmt.Errorf("invalid glob pattern %q: %w", pattern, err)
			}

			patterns = append(patterns, groupKindPath)
			return nil
		})
		if err != nil {
			return
		}

		// Sort the patterns to ensure deterministic choice of wildcard directory for a given GK.
		slices.Sort(patterns)

		globHealthScriptPaths = patterns
	})
	if err != nil {
		return nil, fmt.Errorf("error getting health script glob directories: %w", err)
	}
	return globHealthScriptPaths, nil
}

func getWildcardBuiltInHealthOverrideLua(objKey string) (string, error) {
	// Check if the GVK matches any of the wildcard directories
	globs, err := getGlobHealthScriptPaths()
	if err != nil {
		return "", fmt.Errorf("error getting health script globs: %w", err)
	}
	for _, g := range globs {
		pattern := strings.ReplaceAll(g, "_", "*")
		if !glob.PathMatchUnvalidated(pattern, objKey) {
			continue
		}

		var script []byte
		script, err = resource_customizations.Embedded.ReadFile(filepath.Join(g, healthScriptFile))
		if err != nil {
			return "", fmt.Errorf("error reading %q file in embedded filesystem: %w", filepath.Join(objKey, healthScriptFile), err)
		}
		return string(script), nil
	}
	return "", nil
}

func isValidHealthStatusCode(statusCode health.HealthStatusCode) bool {
	switch statusCode {
	case health.HealthStatusUnknown, health.HealthStatusProgressing, health.HealthStatusSuspended, health.HealthStatusHealthy, health.HealthStatusDegraded, health.HealthStatusMissing:
		return true
	}
	return false
}

// Took logic from the link below and added the int, int32, and int64 types since the value would have type int64
// while actually running in the controller and it was not reproducible through testing.
// https://github.com/layeh/gopher-json/blob/97fed8db84274c421dbfffbb28ec859901556b97/json.go#L154
func decodeValue(l *lua.LState, value any) lua.LValue {
	switch converted := value.(type) {
	case bool:
		return lua.LBool(converted)
	case float64:
		return lua.LNumber(converted)
	case string:
		return lua.LString(converted)
	case json.Number:
		return lua.LString(converted)
	case int:
		return lua.LNumber(converted)
	case int32:
		return lua.LNumber(converted)
	case int64:
		return lua.LNumber(converted)
	case []any:
		arr := l.CreateTable(len(converted), 0)
		for _, item := range converted {
			arr.Append(decodeValue(l, item))
		}
		return arr
	case map[string]any:
		tbl := l.CreateTable(0, len(converted))
		for key, item := range converted {
			tbl.RawSetH(lua.LString(key), decodeValue(l, item))
		}
		return tbl
	case nil:
		return lua.LNil
	}

	return lua.LNil
}
