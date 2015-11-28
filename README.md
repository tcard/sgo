# WARNING: Very early stage!

There's a playground over here: http://fanyare.tcardenas.me:5600

# sgo: a safe Go dialect

SGo is a dialect of the Go programming language that enhances its type system with **optional types** and **removes nil references**. It is based on idiomatic Go patterns, so SGo code feels familiar, and straightforwarldy compiles to plain Go.

It introduces this:

```go
type Response struct {
	Body ?io.ReadCloser
}
// response.Body.Close() doesn't compile; Body might be nil.
if body := response.Body; body != nil {
	body.Close()
}
```

And this:
```go
func Get(r *http.Request) (*Response | error) { ... }
response | err := Get(r)
// body := response.Body doesn't compile; can't use response until we know
// that err is nil.
if err != nil {
	return err
}
body := response.Body
```

While removing this:

```go
var s fmt.Stringer = nil // doesn't compile in SGo; interfaces can't be nil without ?.
s.String() // doesn't compile in SGo; would cause panic.

var m map[string]int = nil // also doesn't compile in SGo, because...
_ = m["needle"]            // ... would cause a panic if used before initialized.
```

## The billion dollar mistake

> "It was the invention of the null reference in 1965. [...] This has led to innumerable errors, vulnerabilities, and system crashes, which have probably caused a billion dollars of pain and damage in the last forty years."
> 
> C. A. R. Hoare, inventor of nil

`nil`, in Go, is used to represent **the idea of "you expected something to be here, but there is nothing"** for certain types (pointers, slices, interfaces, maps, and functions). This is a very useful concept; not only it can be used by the  logic of your programs to express lack of something (e. g. a `http.Request` can have no body, in which case its `Body` field is `nil`), but also provides a meaningful way of initializing variables and fields of those types.

`nil` has a problem, though. Sometimes **the code expect something to _be something_, but it is `nil` instead**. In those situations, **the program usually crashes**; those are the infamous `invalid memory address or nil pointer dereference` panics. There is no way of preventing those situations except carefully reasoning about what do you write, and hoping that testing catches all of This is often not the case. Even when it is, the process of achieving it is costly, if not because of  crashes in production, because of developer effort spent on it.

In SGo, **this nothingness concept is represented in a way that the compiler can track**, so those situations never cause a crash while running the program, but simply prevent the program from even compiling.

## Optional types

In SGo, pointers, interfaces, maps and functions, by themselves can't be `nil`. That is, if you get a pointer, you know that it is pointer somewhere; if you get an interface, you know there's some object implementing it that you can call methods on; if you get a map, you know you can immediately get and put things in it; and, if you get a function, you know you can call it. There's no "hey, I know you asked for this thing but I give you nothing instead". (Slices still can be nil; you can't do anything with it that would cause a nil pointer dereference, so there's no point forbidding that.)

If you do want to provide the option of not having something, you use optional types.

An optional wraps another type. It is denoted by prefixing some other type with a `?`, and it reads like "an optional...", optionally a..." or "maybe a...". Some examples:

```go
func (m *Map) Find(key string) ?*Value  { ... }

func (t *Template) Execute(w io.Writer, data ?interface{}) ?error  { ... }

type TreeNode struct {
	Value interface{}
	Left ?*TreeNode
	Right ?*TreeNode
}
```

There are two kind of values you can assign to an optional: 

* A value of its wrapped type.
* `nil`

```go
var maybeP ?*P // It is initially nil. Nil is an optional's zero value.
maybeP = &P{}  // You can assign a *P to it...
maybeP = nil   // ... and nil back. (Remember, here nil is _not_ a *P.)
```

The key thing about optionals is that you can't do anything with them. You can't call its wrapped type's methods on them, you can't call them if they are wrapping functions, you can put or get stuff in them if they are wrapping a map. Basically, you can't perform any operation that would cause the program to crash if the optional were `nil`. This is the way SGo makes sure this kind of situation doesn't happen.

```go
err := template.Execute(w, 123)
// err has type ?error. The next line wouldn't compile, even if it
// would compile if err had type error.
// fmt.Println(err.Error())
```

The only thing that you can do with an optional is compare them with `nil`. By comparing an optional with `nil`, you can _prove_ that in certain parts of the code an optional does have something instead of nothing, ie. it is not `nil`. Only in those parts you can then use the optional as its wrapped type.

```go
err := template.Execute(w, 123)
if err != nil {
	// Here it is proved that err is not nil. It must be safe to use
	// it as an error instead of as an ?error, e. g. calling its Error()
	// method. SGo's compiler notices that, and it indeed converts err
	// to type error.
	fmt.Println(err.Error())
}
// Back outside of the if, err is again an ?error and you can't do
// anything with it.
```

By returning early, you can also prove that an optional 
```go
func (m *Map) Find(key string) ?*Value  { ... }

maybeNeedle := haystack.Find("needle")
if maybeNeedle == nil {
	return
}
// Because it's a 100% sure thing that we wouldn't reach this point if
// maybeNeedle were nil, we know that there is a needle something in
// maybeNeedle. The compiler thus lets us use maybeNeedle as a *Value
// until the end of the function.
```

To be precise about what "proving" means, and which are those parts of the code in which this is proven:

* In a branch of an `if`, `if-else` or `switch` statement, if the branch condition requires that a variable of type `?T` is not equal to `nil`, then the variable has type `T` instead inside that branch.
* Given an `if`, `if-else` or `switch` statement in a block, if some branch condition requires that a variable in the block's scope of type `?T` is equal to `nil`, and that branch makes the block `return`, `break` or `continue`, then the variable has type `T` instead in every statement below in that same block.

In short, a variable of type `?T` has type `T` instead in a statement if the statement is only reachable when the variable is not `nil`.

## Entangled optionals

It is a very common Go idiom to use multiple returns, such that one of them makes sense only if the other one is `nil`, `true`, or a similarly special value. We see this mainly when returning something may fail:

```go
// Go code; not SGo.
response, err := http.Get("http://github.com/tcard/sgo")
// You could use response here, but if err != nil it wouldn't be pretty.
if err != nil {
	// You shouldn't use response here. It is probably nil.
	return err
}
// It is not enforced by the compiler, but you are told by the documentation
// to trust response not to be nil here, given that err is nil.
```

SGo leverages optionals to also capture this pattern in a safer yet equally convenient manner.

```go
func Get(url string) (*Response | err) { ... }

response, err := http.Get("http://github.com/tcard/sgo")
// You can't use response yet; it wouldn't compile.
if err != nil {
	// You shouldn't use response here. It is probably nil.
	return err
}
// Now it _is_ enforced by the compiler that response is _not_
// nil here.
```

A function signature may have at the end of its return list a vertical bar `|` followed by a type (or a named return with a type), this type being a pointer, map, interface, or function. (It will typically be the `error` interface.)

When calling this function, the last returned value will be an optional. Only once proved, as defined above, that this optional is `nil` you will be able to use the rest of the returned values. (Hence the name "entangled", inspired by _quantum entanglement_, in which collapsing the wavefunction of a particle also causes a collapse in a separate particle that is entangled with it.)

Let's see how to define a function that returns an entangled optional:

```go
func NewRequest(method string, url string, body ?io.Reader) (*Request | error) { ... }

func Get(url string) (*Response | error) {
	req | err := NewRequest("GET", url, nil)
	if err != nil {
		return | err
	}

	response, err = http.DefaultClient.Do(req)
	if err != nil {
		return | err
	}

	return response |
	// Just 'return response | err' would work here too.
}
```

You can entangle any number of variables with an optional, not just one.

```go
func Divide(dividend, divisor int64) (quotient int64, remainder int64 | err error) {
	if divisor == 0 {
		err = errors.New("div by zero")
		return
	}
	quotient = dividend / divisor 
	remainder = dividend % divisor
	return
}
```

## Representation in Go code

Optionals introduce absolutely no runtime costs. You can translate from SGo to Go in your head just by removing the `?`s and the `|`s. When in SGo you assign `nil` to an optional variable, in Go you assign `nil` to a variable of the wrapped type. The only difference is that the resulting Go code is proven to be safe to execute (as in "won't crash due to nil") by the SGo compiler.

This is why only pointers, maps, interfaces and functions can be wrapped in optionals. Those are the types which in Go can be `nil`. SGo keeps Go's feature that memory representation is totally obvious at all points, and doesn't introduce new, unfamiliar memory layouts such as tagged unions. Although it can be handy to have `?string`, or `?int`, that would defeat this purpose. You can either continue to use `""` and `0` or `-1` as nothingness for those types, as you usually do in Go, or wrap them in a pointer in the middle (`?*string`, `?*int`).

## Zero values of pointers, maps, functions and interfaces

In Go, declaring a pointer, map, function or interface without initializing it results in implicitly initializing it to `nil`.

```go
// Go code; not SGo.
var x interface{}
fmt.Println(x) // Prints '<nil>'
```

Those types don't have a zero value in SGo. This is a new situation that never happens in Go, but it is a unavoidable price to pay.

What happens instead is that an uninitialized variable remains uninitialized, and you can't use it until it is proven that you have initialized it. In structs or arrays, you can't leave a field or element of one or those types unitialized.

## Reflection

SGo compiles to Go, and all information about optional types gets lost in translation. This means that reflection will ignore it altogether, and just use the underlying Go representation.

```go
var x ?error
fmt.Printf("%T\n", x) // Prints 'error', not '?error'
```

So you can use reflection to bypass SGo's guarantees.

```go
type Point struct{ X, Y int }
var p *Point = &Point{2, 3}
var pp **Point = &p
v := reflect.ValueOf(pp)
// *pp = nil, which is the same as p = nil.
v.Elem().Set(reflect.Zero(v.Elem().Type()))
// It wouldn't be possible to say p = nil in normal SGo.
fmt.Println(p.X) // Causes a nil panic, because p is nil.
```
