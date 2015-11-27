# WARNING: Very early stage!

There's a playground over here: http://fanyare.tcardenas.me:6500

# sgo: a safe Go dialect

> "It was the invention of the null reference in 1965. [...] This has led to innumerable errors, vulnerabilities, and system crashes, which have probably caused a billion dollars of pain and damage in the last forty years."
> 
> C. A. R. Hoare, inventor of nil

Although Go is statically typed, there are several very common scenarios in which the compiler lets the runtime decide whether some operation should crash. Most of those concern the `nil` value.

SGo is a dialect of Go that moves checking those scenarios to compile time in a idiomatic, minimalist way.

In short: SGo doesn't have `nil` references. Instead, it has optional types and entangled returns.

Let's specify now what those two are. You might want to [take a look at a comparison with Go](#comparison-with-go) first. 

## Optional types

SGo introduces a new kind of type: the optional type. Its syntax is `?T`, where `T` is a type of kind pointer, map, interface, or function.

You can assign two things to a `?T`:

* A value of type `T`.
* `nil`.

`nil` is also the zero value of `T`.

The only operation you can perform on a `?T` is to compare it with `nil`. If you want to use what's maybe inside, you need to prove that there is indeed something.

* In a branch of an `if`, `if-else` or `switch` statement, if the branch condition requires that a variable of type `?T` is not equal to `nil`, then the variable has type `T` instead inside that branch.
* Given an `if`, `if-else` or `switch` statement in a block, if some branch condition requires that a variable in the block's scope of type `?T` is equal to `nil`, and that branch makes the block `return`, `break` or `continue`, then the variable has type `T` instead in every statement below in that same block.

In short, a variable of type `?T` has type `T` instead in a statement if the statement is only reached when the variable is not `nil`.

Example:

```go
type A struct {
	Field int
}

var a &*A

if a != nil {
	// a has type *A here.
	_ = a.Field
}
// Wouldn't compile; a is a &*A here.
// _ = a.Field

if a == nil {
	return
}
// Because a is necessarily non-nil if we reach this statement,
// a is of type *A until the end of the block.
_ = a.Field
```

## Entangled returns

Mutiple return values are a common idiom when we want to use one or other value, but not both. (Typically, the second one is an error.)

```go
if v, err := f(); err != nil {
	// Idiomatically, you shouldn't be supposed to use v here. It's probably a zero value.
}
```

To capture this idiom in the type system, SGo has entangled returns.

A function can have two entangled lists of return values. They are denoted in the signature as two lists of return variables separated by `|`.

TODO: Specify all this.

Example:

```go
func functionThatCanFail() (*A | err) {
	if rand.Intn(2) == 1 {
		return | err
	}
}

a | err := functionThatCanFail()
if err != nil {
	// Can't use a here; can only use err.
} else {
	// Can use a only here; can't use err.
}
// Can't use neither a nor err here.

if err != nil {
	return
}}
// Now you can use a here.

func findMeCoords() (x int, y int | err error)

x, y | err := findMeCoords()
```

## Zero values for pointers, maps, interfaces, functions

Those types don't have zero values in SGo. Instead, they must be initialized.

```go
var r io.Reader
// Can't use r here.
if something {
	f | err = os.Open()
	if err != nil {
		return err
	}
	r = f
} else {
	r
}
// OK, now you can.
r.Read(b) // Won't crash, ever.
```

## Importing Go into SGo

You can import Go packages into SGo. Pointers, maps, interfaces and functions in Go APIs are wrapped in optionals when used from SGo.

## Comparison with Go

Let's look at those occasions in which `nil` can make a Go program crash, and how SGo makes the compiler yell at you instead.

### Dereferencing pointers

What Go does:

```go
var a *A // its value is nil
a = nil // no change
if someCondition {
	a = &A{}
}
_ = *a // crashes if a is still nil
_ = a.Field // crashes if a is still nil
```

What SGo does:

```go
var a *A // Doesn't have a value yet.
// Doesn't compile; *A can't be nil.
// a = nil
if someCondition {
	a = &A{}
}
// Doesn't compile; a might not have a value yet at this point.
// _ = *a
// _ = a.Field
```

How you do that in SGo:

```go
var a ?*A // Its value is nil.
a = nil // No change here.
if someCondition {
	a = &A{}
}
// Doesn't compile; type ?*A can't be dereferenced.
// _ = *a
// _ = a.Field

if a != nil {
	// Inside this block, a has type *A, which _can't_ be nil.
	_ = *a // OK; won't crash ever.
}

if a == nil {
	return
}

// Because of the return above, from this point a has type *A.
_ = *a // OK; won't crash ever.
_ = a.Field // OK; won't crash ever.
```

## Alternatives

This design introduces several rather radical new concepts to Go:

* Uninitialized variables. `var x T` is either zero-valued or unitialized depending on the kind of `T`.
* Variables that change their type in the same scope.

Those can potentially enable complex behavior. On the other hand:

* Strict separation of statements and expressions of Go is kept.
* Minimal new syntax is introduced. Specially, there is no new syntax for pattern-matching.
* Code probably will look like very similar to Go. Idioms are kept.

We could avoid both if we made `if` statements be expressions and had some syntax to pattern-match.

```go
// var p *Pointer -- doesn't compile
var p *Pointer = if something { &A } else { &B }

var err ?error = somethingThatMightFail()
if x := err? {
	// x has type error here
}
// x undeclared here
```

```
err := if err := somethingThatMightFail() { err } else { return }
// err has type error here, not ?error
```