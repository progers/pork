package pork

import (
  "errors"
  "fmt"
  "net/http"
  "io"
  "os"
  "path"
  "path/filepath"
  "strings"
)

// todo: implement Content.Productionize
// todo: implement multi-mount.
type OptimizationLevel int
const (
  None OptimizationLevel = iota
  Basic
  Advanced
)

const (
  porkScriptFileExtension = ".pork.js"
  javaScriptFileExtension = ".js"
)

var PathToCpp = "/usr/bin/cpp"
var PathToJava = "/usr/bin/java"

var rootDir string

func pathToJsc() string {
  return filepath.Join(rootDir, "compiler.jar")
}

func waitFor(p *os.Process) error {
  s, err := p.Wait(0)
  if err != nil {
    return err
  }

  if sc := s.WaitStatus.ExitStatus(); sc != 0 {
    return errors.New(fmt.Sprintf("exit code: %d", sc))
  }

  return nil
}

func cpp(filename string, w *os.File) (*os.Process, error) {
  cppArgs := []string{
      PathToCpp,
      "-P",
      "-CC",
      fmt.Sprintf("-I%s", filepath.Join(rootDir, "js")),
      filename}
  return os.StartProcess(cppArgs[0],
      cppArgs,
      &os.ProcAttr{
          "",
          os.Environ(),
          []*os.File{nil, w, os.Stderr},
      nil})
}

func jsc(r *os.File, w *os.File, jscPath string, level OptimizationLevel) (*os.Process, error) {
  jvmArgs := []string{PathToJava, "-jar", jscPath}
  if level == Advanced {
    jvmArgs = append(jvmArgs, "--compilation_level", "ADVANCED_OPTIMIZATIONS")
  }

  return os.StartProcess(jvmArgs[0],
    jvmArgs,
    &os.ProcAttr{
      "",
      os.Environ(),
      []*os.File{r, w, os.Stderr},
      nil})
}

type content struct {
  root []http.Dir
  level OptimizationLevel
}

func Init(root string) {
  r, err := filepath.Abs(root)
  if err != nil {
    panic(err)
  }
  rootDir = r
}

func Content(level OptimizationLevel, d ...http.Dir) http.Handler {
  return &content{d, level}
}

func expandPath(fs http.Dir, name string) string {
  return filepath.Join(string(fs), filepath.FromSlash(path.Clean("/" + name)))
}

func findFile(d []http.Dir, name string) (string, bool) {
  for i, n := 0, len(d); i < n; i++ {
    target := filepath.Join(string(d[i]), filepath.FromSlash(path.Clean("/" + name)))
    if _, err := os.Stat(target); err == nil {
      return target, true
    }
  }
  return "", false
}

func ServeContent(w http.ResponseWriter, r *http.Request, level OptimizationLevel, d ...http.Dir) {
  path := r.URL.Path

  // if the file exists, just serve it.
  if target, found := findFile(d, path); found {
    http.ServeFile(w, r, target)
    return
  }

  // if the missing file isn't a special one, 404.
  if !strings.HasSuffix(path, javaScriptFileExtension) {
    ServeNotFound(w, r)
    return
  }

  source, found := findFile(d, path[0 : len(path) - len(javaScriptFileExtension)] + porkScriptFileExtension)
  if !found {
    ServeNotFound(w, r)
    return
  }

  w.Header().Set("Content-Type", "text/javascript")
  err := Compile(source, w, level)
  if err != nil {
    // todo: send to ServeSiteError()
    panic(err)
  }
}

func (h *content) ServeHTTP(w http.ResponseWriter, r *http.Request) {
  ServeContent(w, r, h.level, h.root...)
}

func ServeNotFound(w http.ResponseWriter, r *http.Request) {
  http.NotFound(w, r)
}

func Compile(filename string, w io.Writer, level OptimizationLevel) error {
  // output pipe
  orp, owp, err := os.Pipe()
  if err != nil {
    return err
  }
  defer orp.Close()
  defer owp.Close()

  var cp *os.Process
  switch level {
  case None:
    cp, err = cpp(filename, owp)
    if err != nil {
      return err
    }
    owp.Close()
  case Basic, Advanced:
    irp, iwp, err := os.Pipe()
    if err != nil {
      return err
    }
    defer irp.Close()
    defer iwp.Close()

    cp, err = cpp(filename, iwp)
    if err != nil {
      return err
    }

    iwp.Close()

    jp, err := jsc(irp, owp, pathToJsc(), level)
    if err != nil {
      return err
    }

    irp.Close()
    owp.Close()

    err = waitFor(jp)
    if err != nil {
      return err
    }
  }

  err = waitFor(cp)
  if err != nil {
    return err
  }

  _, err = io.Copy(w, orp)
  if err != nil {
    return err
  }

  return nil
}
