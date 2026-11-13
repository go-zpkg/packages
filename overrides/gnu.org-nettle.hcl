project = "gnu.org/nettle"
why     = "nettle links libraries it did not declare, so an artifact installed into an empty store cannot start."

edits = [
  {
    path = "dependencies"
    set = {
      "gnu.org/gmp" = "^6"
    }
  },
]
