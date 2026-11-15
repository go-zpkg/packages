project = "gnu.org/grep"
why     = "Building GNU grep needs a working GNU grep: its own configure refuses every other one, macOS's BSD grep included, with `configure: error: no working 'grep' found`. zpkb keeps a package's own artifact out of the BASE toolchain of its own build (go-zpkg/zpkb#105), so grep has to ask for itself here. Scaffolding, not policy."

edits = [
  {
    path = "build.dependencies[\"gnu.org/grep\"]"
    set  = "*"
  },
]
