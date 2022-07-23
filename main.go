package main

import (
    "context"
    "errors"
    "flag"
    "fmt"
    "os"

    "github.com/natanbc/ssc0904-nat-traversal/client"
    "github.com/natanbc/ssc0904-nat-traversal/coord"

    "github.com/peterbourgon/ff/v3/ffcli"
)

func main() {
    self := os.Args[0]
    args := os.Args[1:]

    root := &ffcli.Command {
        Name:        self,
        ShortUsage:  fmt.Sprintf("%s <subcommand> [flags]", self),
        LongHelp:    fmt.Sprintf(`For help on subcommands, add --help after: "%s subcommand --help".`, self),
        Subcommands: []*ffcli.Command {
            client.Command,
            coord.Command,
        },
        Exec:        func(context.Context, []string) error { return flag.ErrHelp },
    }

    if err := root.Parse(args); err != nil {
        fmt.Fprintf(os.Stderr, "%v\n", err)
        os.Exit(1)
    }

    if err := root.Run(context.Background()); err != nil {
        if errors.Is(err, flag.ErrHelp) {
            return
        }
        panic(err)
    }
}
