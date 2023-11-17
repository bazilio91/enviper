package enviper

import (
	"encoding/json"
	"fmt"
	"github.com/mitchellh/mapstructure"
	"os"
	"reflect"
	"strings"
	"time"
	"unsafe"

	"github.com/spf13/viper"
)

// Enviper is a wrapper struct for viper,
// that makes it possible to unmarshal config to struct
// considering environment variables
type Enviper struct {
	*viper.Viper
	tagName string
}

// New returns an initialized Enviper instance
func New(v *viper.Viper) *Enviper {
	return &Enviper{
		Viper: v,
	}
}

const defaultTagName = "mapstructure"

// WithTagName sets custom tag name to be used instead of default `mapstructure`
func (e *Enviper) WithTagName(customTagName string) *Enviper {
	e.tagName = customTagName
	return e
}

// TagName returns currently used tag name (`mapstructure` by default)
func (e *Enviper) TagName() string {
	if e.tagName == "" {
		return defaultTagName
	}
	return e.tagName
}

func SliceDecodeHook() mapstructure.DecodeHookFuncType {
	return func(
		f reflect.Type, // data type
		t reflect.Type, // target data type
		data interface{}, // raw data
	) (interface{}, error) {
		// Check if the data type matches the expected one
		if f.Kind() != reflect.String {
			return data, nil
		}

		if t.Kind() != reflect.Slice {
			return data, nil
		}

		// Construct instance by reflect & unmarshal
		target := reflect.New(t).Interface()
		err := json.Unmarshal([]byte(data.(string)), &target)
		if err != nil {
			return data, nil
		}

		return target, nil
	}
}

// Unmarshal unmarshals the config into a Struct just like viper does.
// The difference between enviper and viper is in automatic overriding data from file by data from env variables
func (e *Enviper) Unmarshal(rawVal interface{}, opts ...viper.DecoderConfigOption) error {
	opts = append(opts, viper.DecodeHook(SliceDecodeHook()))

	if e.TagName() != defaultTagName {
		opts = append(opts, func(c *mapstructure.DecoderConfig) {
			c.TagName = e.TagName()
		})
	}

	if err := e.Viper.ReadInConfig(); err != nil {
		switch err.(type) {
		case viper.ConfigFileNotFoundError:
			// 	do nothing
		default:
			return err
		}
	}
	// We need to unmarshal before the env binding to make sure that keys of maps are bound just like the struct fields
	// We silence errors here because we'll unmarshal a second time
	_ = e.Viper.Unmarshal(rawVal, opts...)
	e.readEnvs(rawVal)
	return e.Viper.Unmarshal(rawVal, opts...)
}

func (e *Enviper) readEnvs(rawVal interface{}) {
	e.Viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	e.bindEnvs(rawVal)
}

func (e *Enviper) bindEnvs(in interface{}, prev ...string) {
	ifv := reflect.ValueOf(in)
	if ifv.Kind() == reflect.Ptr {
		ifv = ifv.Elem()
	}

	switch ifv.Kind() {
	case reflect.Struct:
		for i := 0; i < ifv.NumField(); i++ {
			fv := ifv.Field(i)
			if fv.Kind() == reflect.Ptr {
				if fv.IsZero() {
					fv = reflect.New(fv.Type().Elem()).Elem()
				} else {
					fv = fv.Elem()
				}
			}
			t := ifv.Type().Field(i)
			tv, ok := t.Tag.Lookup(e.TagName())
			if ok {
				if index := strings.Index(tv, ","); index != -1 {
					if tv[:index] == "-" {
						continue
					}

					// If "squash" is specified in the tag, we squash the field down.
					if strings.Contains(tv[index+1:], "squash") {
						e.bindEnvs(fv.Interface(), prev...)
						continue
					}

					tv = tv[:index]
				}

				if tv == "" {
					tv = t.Name
				}
			} else {
				tv = t.Name
			}

			if fv.CanInterface() {
				e.bindEnvs(fv.Interface(), append(prev, tv)...)
			}
		}
	case reflect.Map:
		iter := ifv.MapRange()
		for iter.Next() {
			if key, ok := iter.Key().Interface().(string); ok {
				e.bindEnvs(iter.Value().Interface(), append(prev, key)...)
			}
		}
	case reflect.Slice:
		env := strings.Join(prev, ".")
		_ = e.Viper.BindEnv(env)

		key := env

		rs := reflect.ValueOf(e.Viper).Elem().FieldByName("envKeyReplacer")
		envKeyReplacer := reflect.NewAt(rs.Type(), unsafe.Pointer(rs.UnsafeAddr())).Interface().(*viper.StringReplacer)

		if *envKeyReplacer != nil {
			key = (*envKeyReplacer).Replace(key)
		}

		envPrefix := reflect.ValueOf(e.Viper).Elem().FieldByName("envPrefix").String()

		if envPrefix != "" {
			key = strings.ToUpper(envPrefix + "_" + key)
		}

		key = strings.ToUpper(key)

		envs := os.Environ()

		values := []string{}
		for _, s := range envs {
			if strings.HasPrefix(s, fmt.Sprintf("%s_", key)) {
				k := strings.Split(s, "=")[0]
				values = append(values, os.Getenv(k))
			}
		}

		tp, castedByViper := supportedCast(in)

		e.Viper.SetDefault(env, tp)

		if castedByViper {
			os.Setenv(key, strings.Join(values, " "))
		} else {
			// Marshal slice into json to be decoded by hook
			decodedValues := []interface{}{}
			for _, str := range values {
				var decodedValue interface{}
				err := json.Unmarshal([]byte(str), &decodedValue)
				if err != nil {
					decodedValues = append(decodedValues, str)
				} else {
					decodedValues = append(decodedValues, decodedValue)
				}
			}

			data, _ := json.Marshal(decodedValues)
			os.Setenv(key, string(data))
		}
	default:
		env := strings.Join(prev, ".")
		// Viper.BindEnv will never return error
		// because env is always non empty string
		_ = e.Viper.BindEnv(env)
	}
}

func supportedCast(in interface{}) (interface{}, bool) {
	castedByViper := true
	var tp interface{}
	switch t := in.(type) {
	case bool:
		tp = t
	case string:
		tp = t
	case int32, int16, int8, int:
		tp = t
	case uint:
		tp = t
	case uint32:
		tp = t
	case uint64:
		tp = t
	case int64:
		tp = t
	case float64, float32:
		tp = t
	case time.Time:
		tp = t
	case time.Duration:
		tp = t
	case []string:
		tp = t
	case []int:
		tp = t
	default:
		castedByViper = false
		tp = t
	}

	return tp, castedByViper
}
