package external

import (
	"context"
	"fmt"
)

type CalcArgs struct {
	Operator     string  `json:"operator"`
	FirstNumber  float64 `json:"first_number"`
	SecondNumber float64 `json:"second_number"`
}

func Calculator(ctx context.Context, args CalcArgs) (float64, error) {
	switch args.Operator {
	case "add":
		return args.FirstNumber + args.SecondNumber, nil
	case "subtract":
		return args.FirstNumber - args.SecondNumber, nil
	case "multiply":
		return args.FirstNumber * args.SecondNumber, nil
	case "divide":
		return args.FirstNumber / args.SecondNumber, nil
	default:
		return 0, fmt.Errorf("unknown operator: %s", args.Operator)
	}
}
