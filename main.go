// Copyright (c) 2018 Miłosz Skaza

// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:

// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.

// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/julienschmidt/httprouter"

	yaml "gopkg.in/yaml.v2"
)

// Config defines routes and other stuff
type Config struct {
	Routes                 map[string]Route `yaml:"routes"`
	Address                string           `yaml:"address"`
	Debug                  bool             `yaml:"debug"`
	NotFoundRedirect       string           `yaml:"not_found_redirect,omitempty"`
	NotFoundRedirectStatus int              `yaml:"not_found_redirect_status,omitempty"`
}

// CompareFunc enforces structure of underlaying comparing functions
type CompareFunc func(a, b string) bool

// Condition contains raw and parsed contents of a declared condition
// Condition has to be Parse(d) before usage
// It contains a CompareFunc with is of type CompareFunc and
// might be used with values to check whether they fulfil the condition
type Condition struct {
	Raw         string
	Type        string      `yaml:"-"`
	Expected    string      `yaml:"-"`
	CompareFunc CompareFunc `yaml:"-"`
}

// UnmarshalYAML makes condition implement yaml.Marshaller to work properly
func (c *Condition) UnmarshalYAML(unmarshal func(interface{}) error) error {
	raw := ""
	err := unmarshal(&raw)
	if err != nil {
		return err
	}

	c.Raw = raw
	return nil
}

// Parse populates the condition
func (c *Condition) Parse() {
	expr := strings.Split(c.Raw, " ")
	value := expr[0]
	operator := expr[1]
	expected := expr[2]

	var compareFunc CompareFunc

	switch value {
	case "User-Agent":
		switch operator {
		case "has":
			compareFunc = c.contains
		case "is":
			compareFunc = c.isEqual
		case "starts_with":
			compareFunc = c.hasPrefix
		case "ends_with":
			compareFunc = c.hasSuffix
		default:
			log.Println("Improperly configured condition:", c.Raw)
		}

	case "Time":
		switch operator {
		case "lt":
			compareFunc = c.timeBefore
		case "gt":
			compareFunc = c.timeAfter
		default:
			log.Println("Improperly configured condition:", c.Raw)
		}
	default:
		log.Println("Improperly configured condition:", c.Raw)
	}

	c.Expected = expected
	c.Type = value
	c.CompareFunc = compareFunc
}

// This wrapping of strings.* functions is necessary or pointers get lost
func (c Condition) contains(a, b string) bool {
	return strings.Contains(a, b)
}

func (c Condition) isEqual(a, b string) bool {
	return a == b
}

func (c Condition) hasPrefix(a, b string) bool {
	return strings.HasPrefix(a, b)
}

func (c Condition) hasSuffix(a, b string) bool {
	return strings.HasSuffix(a, b)
}

func (c Condition) timeBefore(a, b string) bool {
	t1, err := time.Parse(time.RFC3339, a)
	if err != nil {
		log.Println("T1 parsing error:", err)
		return false
	}

	t2, err := time.Parse(time.RFC3339, b)
	if err != nil {
		log.Println("T2 parsing error:", err)
		return false
	}

	if t1.Before(t2) {
		return true
	}

	return false
}

func (c Condition) timeAfter(a, b string) bool {
	t1, err := time.Parse(time.RFC3339, a)
	if err != nil {
		log.Println("T1 parsing error:", err)
		return false
	}

	t2, err := time.Parse(time.RFC3339, b)
	if err != nil {
		log.Println("T2 parsing error:", err)
		return false
	}

	if t2.Before(t1) {
		return true
	}

	return false
}

// Route is the main structure of the application containing information about
// one route with conditions, methods and success/failure redirects
// It should be Unmarshalled from YAML
type Route struct {
	Path            string       `yaml:"path"`
	Conditions      []*Condition `yaml:"conditions"`
	AllowedMethods  []string     `yaml:"allowed_methods"`
	SuccessRedirect string       `yaml:"success_redirect"`
	FailureRedirect string       `yaml:"failure_redirect"`
	RedirectStatus  int          `yaml:"redirect_status"`
}

// ParseConditions parses all the defined raw conditions in a route
func (r *Route) ParseConditions() {
	for _, condition := range r.Conditions {
		condition.Parse()
	}
}

// BuildHandler creates httprouter.Handle function to do the routing with
// the data specified on the route
func (r Route) BuildHandler() httprouter.Handle {
	return func(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
		for _, condition := range r.Conditions {
			switch condition.Type {
			case "User-Agent":
				if config.Debug {
					log.Println("Checking", condition.Type, ", Got:", req.Header.Get("User-Agent"), "Expected:", condition.Expected)
					log.Println("Evaluates to:", condition.CompareFunc(req.Header.Get("User-Agent"), condition.Expected))
				}

				if !condition.CompareFunc(req.Header.Get("User-Agent"), condition.Expected) {
					http.Redirect(w, req, r.FailureRedirect, r.RedirectStatus)
					return
				}

			case "Time":
				if config.Debug {
					log.Println("Time condition evaluates to:", condition.CompareFunc(time.Now().Format(time.RFC3339), condition.Expected))
				}
				if !condition.CompareFunc(time.Now().Format(time.RFC3339), condition.Expected) {
					http.Redirect(w, req, r.FailureRedirect, r.RedirectStatus)
					return
				}
			}
		}

		// If all the checks have passed and not returned it's safe to redirect
		http.Redirect(w, req, r.SuccessRedirect, r.RedirectStatus)
		return
	}
}

var config Config

func init() {
	file, err := ioutil.ReadFile("./config.yaml")
	if err != nil {
		log.Panicln("Cannot find config.yaml:", err)
	}

	config = Config{}
	err = yaml.Unmarshal(file, &config)
	if err != nil {
		log.Panicln("Failed loading config:", err)
	}

	fmt.Println("Loaded routes: ")
	for route, conf := range config.Routes {
		fmt.Println(route, "-->", conf.SuccessRedirect, "||", "x", "-->", conf.FailureRedirect)
		fmt.Println(" ", strings.Join(conf.AllowedMethods, ", "))
		for _, cond := range conf.Conditions {
			fmt.Println("   ", cond.Raw)
		}
		fmt.Println()
	}

}

func main() {
	router := httprouter.New()

	if config.NotFoundRedirect != "" && config.NotFoundRedirectStatus != 0 {
		fmt.Println("Not found redirect is ON. Redirecting to", config.NotFoundRedirect, "with status", config.NotFoundRedirectStatus)
		router.NotFound = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, config.NotFoundRedirect, config.NotFoundRedirectStatus)
		})
	} else {
		fmt.Println("Not found redirect is OFF. Returning 404s.")
	}
	// Test routes, feel free to delete them
	router.GET("/panel", func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		fmt.Fprint(w, "Hello user, how are you?")
	})
	router.GET("/bye", func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		fmt.Fprint(w, "Nothing here! Bye!!!")
	})

	for path, route := range config.Routes {
		route.ParseConditions()
		for _, method := range route.AllowedMethods {
			router.Handle(method, path, route.BuildHandler())
		}
	}

	fmt.Println("Server started on port", config.Address)
	log.Fatal(http.ListenAndServe(config.Address, router))
}
