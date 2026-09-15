module benchmark

go 1.24.0

require (
	github.com/SchwarzDigits/hypermatch/v2 v2.0.0
	quamina.net/go/quamina v1.5.1
)

// Always benchmark the code in this repository.
replace github.com/SchwarzDigits/hypermatch/v2 => ../
