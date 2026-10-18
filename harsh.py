#!/usr/bin/env python3
"""
calculator.py — a simple, safe command-line calculator.

Usage:
    python calculator.py              # interactive mode
    python calculator.py "2 + 3 * 4"  # evaluate one expression

Supports:
    operators:  +  -  *  /  //  %  **  and parentheses
    functions:  sqrt, abs, round, sin, cos, tan, log, log10, exp
    constants:  pi, e

Expressions are parsed with the `ast` module and evaluated against a
whitelist of allowed operations, so arbitrary code cannot be executed.
"""

import ast
import math
import operator
import sys

_BIN_OPS = {
    ast.Add: operator.add,
    ast.Sub: operator.sub,
    ast.Mult: operator.mul,
    ast.Div: operator.truediv,
    ast.FloorDiv: operator.floordiv,
    ast.Mod: operator.mod,
    ast.Pow: operator.pow,
}

_UNARY_OPS = {
    ast.UAdd: operator.pos,
    ast.USub: operator.neg,
}

_FUNCTIONS = {
    "sqrt": math.sqrt,
    "abs": abs,
    "round": round,
    "sin": math.sin,
    "cos": math.cos,
    "tan": math.tan,
    "log": math.log,
    "log10": math.log10,
    "exp": math.exp,
}

_CONSTANTS = {
    "pi": math.pi,
    "e": math.e,
}


def evaluate(expression):
    """Safely evaluate a math expression and return the result."""
    try:
        tree = ast.parse(expression, mode="eval")
    except SyntaxError as exc:
        raise ValueError("invalid expression: {}".format(exc.msg)) from None
    return _eval(tree.body)


def _eval(node):
    if isinstance(node, ast.Constant) and isinstance(node.value, (int, float)):
        return node.value
    if isinstance(node, ast.BinOp) and type(node.op) in _BIN_OPS:
        left = _eval(node.left)
        right = _eval(node.right)
        return _BIN_OPS[type(node.op)](left, right)
    if isinstance(node, ast.UnaryOp) and type(node.op) in _UNARY_OPS:
        return _UNARY_OPS[type(node.op)](_eval(node.operand))
    if isinstance(node, ast.Call) and isinstance(node.func, ast.Name):
        if node.keywords:
            raise ValueError("keyword arguments are not supported")
        name = node.func.id
        if name not in _FUNCTIONS:
            raise ValueError("unknown function: {}".format(name))
        args = [_eval(arg) for arg in node.args]
        return _FUNCTIONS[name](*args)
    if isinstance(node, ast.Name) and node.id in _CONSTANTS:
        return _CONSTANTS[node.id]
    raise ValueError("unsupported expression element: {}".format(type(node).__name__))


def _format(result):
    """Render a result, dropping a trailing .0 on whole-number floats."""
    if isinstance(result, float) and result.is_integer() and abs(result) < 1e15:
        return str(int(result))
    return str(result)


def main(argv):
    if len(argv) > 1:  # one-shot mode: calculator.py "1 + 2"
        expression = " ".join(argv[1:])
        try:
            print(_format(evaluate(expression)))
        except (ValueError, ZeroDivisionError) as exc:
            print("error: {}".format(exc), file=sys.stderr)
            return 1
        return 0

    print("calculator — enter an expression, or 'quit' to exit")
    while True:
        try:
            line = input(">> ").strip()
        except (EOFError, KeyboardInterrupt):
            print()
            return 0
        if not line:
            continue
        if line.lower() in {"quit", "exit"}:
            return 0
        try:
            print(_format(evaluate(line)))
        except (ValueError, ZeroDivisionError) as exc:
            print("error: {}".format(exc), file=sys.stderr)


if __name__ == "__main__":
    sys.exit(main(sys.argv))
