import { Kind, parse, type OperationTypeNode } from "graphql";
import type { OperationKind } from "./types.ts";

/**
 * Kind of the operation that would execute for `document`. With several operations, `operationName` must pick one.
 * Throws on syntax errors, so callers can reject unsafe documents before any request is made.
 */
export function operationKind(document: string, operationName?: string): OperationKind {
  const ops = parse(document, { noLocation: true }).definitions.filter((d) => d.kind === Kind.OPERATION_DEFINITION);
  if (ops.length === 0) throw new Error("document contains no operation");
  let op = ops[0];
  if (operationName) {
    op = ops.find((o) => o.name?.value === operationName);
    if (!op) throw new Error(`operation "${operationName}" not found in document`);
  } else if (ops.length > 1) {
    throw new Error(`document has ${ops.length} operations; pass operationName`);
  }
  const kind: OperationTypeNode = op!.operation;
  return kind;
}
