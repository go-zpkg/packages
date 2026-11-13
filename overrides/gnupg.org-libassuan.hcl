project = "gnupg.org/libassuan"
why     = "libassuan declares what its BUILD runs and not what the result LINKS, so a consumer installing the artifact into an empty store is missing libraries nothing named."

edits = [
  {
    path = "dependencies"
    set = {
      "gnupg.org/libgpg-error" = "^1"
    }
  },
]
