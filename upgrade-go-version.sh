#!/bin/bash

FROM="go1.8.3"
TO="go1.10.2"

PKGS=(
	"ast"
	"constant"
	"doc"
	"format"
	"internal/format"
	"internal/testenv"
	"parser"
	"printer"
	"scanner"
	"token"
	"types"
)

SGOSRC=`pwd`

function last-commit {
	git log | head -n 1 | awk '{print $2}'
}

function checkout-go {
	echo "Checking out $1."

	cd /usr/local/go
	git checkout $1
	for dir in ${PKGS[@]}; do
		cp -r ./src/go/$dir/ $SGOSRC/sgo/$dir
	done
	cd $SGOSRC
	git add ./sgo
	git commit -m "[upgrade-go-version.sh] Checkout $1."
}

checkout-go $FROM
FROMCOMMIT=`last-commit`

echo "Getting commit with SGo changes on top of $FROM."
git revert --no-edit HEAD
git commit --amend -m "[upgrade-go-version.sh] Apply SGo changes on top of $FROM."
SGOCOMMIT=`last-commit`

git reset --hard $FROMCOMMIT
checkout-go $TO

echo "Cherry-picking SGo changes from $FROM on top of $TO."
git cherry-pick $SGOCOMMIT

echo "Fix conflcits, then squash everything, then ????, then profit!"
