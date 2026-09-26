#!/bin/sh
set -e
cd "$(dirname "$0")"

fail() { printf '\n  %s\n\n' "$1" >&2; exit 1; }

branch=$(git rev-parse --abbrev-ref HEAD)
[ "$branch" = main ] || fail "On $branch. Releases are cut from main."
[ -z "$(git status --porcelain)" ] || fail "Working tree is dirty. Commit or stash before releasing."

git fetch --quiet origin --tags
set -- $(git rev-list --left-right --count main...origin/main)
[ "$2" = 0 ] || fail "main is $2 commit(s) behind origin/main. Pull first."

latest=$(git tag --list 'v*' --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -1 || true)
if [ -n "$latest" ]; then
  IFS=. read -r major minor patch <<EOT
${latest#v}
EOT
  recommended=$major.$minor.$((patch + 1))
else
  recommended=0.1.0
fi

printf '\n  main       %s\n' "$(git log -1 --format='%h  %s')"
printf '  latest tag %s\n\n' "${latest:-none}"
if [ -n "$latest" ]; then
  printf '    patch  %s   (recommended)\n' "$recommended"
  printf '    minor  %s\n' "$major.$((minor + 1)).0"
  printf '    major  %s\n\n' "$((major + 1)).0.0"
fi

printf '  Next version [%s]: ' "$recommended"
read -r version
version=${version:-$recommended}
version=${version#v}
echo "$version" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$' || fail "\"$version\" is not a version of the form MAJOR.MINOR.PATCH."
tag=v$version
git rev-parse -q --verify "refs/tags/$tag" >/dev/null && fail "$tag already exists. Releasing over a tag rewrites what it means."

printf '\n  This will:\n'
[ "$1" = 0 ] || printf '    - push main to origin\n'
printf '    - tag %s and push it, which builds and publishes a release\n' "$tag"
printf '\n  Release %s? [y/N]: ' "$tag"
read -r ok
case $ok in
  y|Y) ;;
  *) printf '\n  Nothing was pushed.\n\n'; exit 0 ;;
esac

git push --quiet origin main
git tag -a "$tag" -m "sipline $version"
git push --quiet origin "$tag"

remote=$(git remote get-url origin)
remote=${remote%.git}
printf '  Pushed %s.\n\n' "$tag"
printf '  Build:   %s/actions\n' "$remote"
printf '  Release: %s/releases/tag/%s\n\n' "$remote" "$tag"
