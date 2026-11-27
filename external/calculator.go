package external

import (
	"context"
	"fmt"
	"math"
)

type CalcArgs struct {
	Operator     string  `json:"operator"`
	FirstNumber  float64 `json:"first_number"`
	SecondNumber float64 `json:"second_number"`
}

func Calculator(ctx context.Context, args CalcArgs) (float64, error) {
	var result float64
	switch args.Operator {
	case "add":
		result = args.FirstNumber + args.SecondNumber
	case "subtract":
		result = args.FirstNumber - args.SecondNumber
	case "multiply":
		result = args.FirstNumber * args.SecondNumber
	case "divide":
		result = args.FirstNumber / args.SecondNumber
	default:
		return 0, fmt.Errorf("unknown operator: %s", args.Operator)
	}
	// divide by zero and overflow give +Inf, -Inf or NaN: refuse them instead of passing them to the model.
	if math.IsInf(result, 0) || math.IsNaN(result) {
		return 0, fmt.Errorf("%s of %v and %v is not a finite number", args.Operator, args.FirstNumber, args.SecondNumber)
	}
	return result, nil
}
