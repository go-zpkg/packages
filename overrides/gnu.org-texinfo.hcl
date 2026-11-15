project = "gnu.org/texinfo"
why     = "texinfo's perl XS link never sees zpkb's LDFLAGS, so the extension is built without the rpath it needs and the result cannot find its own libraries."

edits = [
  {
    path = "build.script"
    prepend = [
      {
        "if"  = "darwin"
        "run" = <<EOT
mkdir -p "$SRCROOT/.zpkb-cc"
printf '#!/bin/sh\nexec /usr/bin/cc -Wl,-headerpad_max_install_names -Wl,-rpath,{{pkgx.prefix}} "$@"\n' > "$SRCROOT/.zpkb-cc/cc"
chmod +x "$SRCROOT/.zpkb-cc/cc"
export PATH="$SRCROOT/.zpkb-cc:$PATH"
export CC="$SRCROOT/.zpkb-cc/cc"
EOT
      },
    ]
  },
]
