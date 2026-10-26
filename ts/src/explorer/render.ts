import {
  DEFAULT_DEPRECATION_REASON,
  isEnumType,
  valueToLiteral,
  isInputObjectType,
  isInterfaceType,
  isObjectType,
  isScalarType,
  isUnionType,
  type ConstDirectiveNode,
  type ConstValueNode,
  type GraphQLArgument,
  type GraphQLEnumValue,
  type GraphQLField,
  type GraphQLInputField,
  type GraphQLNamedType,
} from "graphql";
import type { ExplorerElement } from "./graph.ts";

// Go's json.Marshal uses JSON escapes for GraphQL string literals and escapes
// HTML characters and the two JavaScript line separators by default.
function quoted(value: string): string {
  return JSON.stringify(value).replace(/[<>&\u2028\u2029]/g, (char) => {
    switch (char) {
      case "<": return "\\u003c";
      case ">": return "\\u003e";
      case "&": return "\\u0026";
      case "\u2028": return "\\u2028";
      default: return "\\u2029";
    }
  });
}

function literal(value: ConstValueNode, compact = false): string {
  const separator = compact ? "," : ", ";
  switch (value.kind) {
    case "StringValue": return quoted(value.value);
    case "ListValue": return `[${value.values.map((entry) => literal(entry, compact)).join(separator)}]`;
    case "ObjectValue": return `{${value.fields.map((field) => `${field.name.value}:${compact ? "" : " "}${literal(field.value, compact)}`).join(separator)}}`;
    case "NullValue": return "null";
    case "BooleanValue": return value.value ? "true" : "false";
    default: return value.value;
  }
}
function directives(nodes: ReadonlyArray<ConstDirectiveNode> | undefined | null, deprecationReason?: string | null, fallback = ""): string {
  if (nodes) {
    return nodes.map((node) => ` @${node.name.value}${node.arguments?.length ? `(${node.arguments.map((arg) => `${arg.name.value}: ${literal(arg.value)}`).join(", ")})` : ""}`).join("");
  }
  if (deprecationReason != null) {
    return ` @deprecated${deprecationReason === DEFAULT_DEPRECATION_REASON ? "" : `(reason: ${quoted(deprecationReason)})`}`;
  }
  return fallback;
}

function description(text: string | null | undefined, depth: number, includeDescription: boolean): string {
  return includeDescription && text ? `${"  ".repeat(depth)}${quoted(text)}\n` : "";
}

type InputValue = GraphQLArgument | GraphQLInputField;

function defaultNode(input: InputValue): ConstValueNode | null {
  return (input.astNode?.defaultValue ?? input.default?.literal ??
    (input.default && "value" in input.default ? valueToLiteral(input.default.value, input.type) :
      input.defaultValue === undefined ? null : valueToLiteral(input.defaultValue, input.type))) ?? null;
}

/** The cached default encoded like gqlparser's Value.String(), if present. */
export function defaultValueLiteral(input: InputValue): string | null {
  const node = defaultNode(input);
  return node ? literal(node, true) : null;
}

function defaultValue(input: InputValue): string {
  const node = defaultNode(input);
  return node ? ` = ${literal(node)}` : "";
}

function inputValue(input: InputValue, depth: number, includeDescription: boolean): string {
  return `${description(input.description, depth, includeDescription)}${"  ".repeat(depth)}${input.name}: ${input.type}${defaultValue(input)}${directives(input.astNode?.directives, input.deprecationReason)}${depth ? "\n" : ""}`;
}

function field(value: GraphQLField<unknown, unknown>, depth: number, includeDescription: boolean): string {
  const args = value.args;
  let printedArgs = "";
  if (args.length) {
    const multiline = includeDescription && args.some((arg) => !!arg.description);
    printedArgs = multiline
      ? `(\n${args.map((arg) => inputValue(arg, depth + 1, includeDescription)).join("")}${"  ".repeat(depth)})`
      : `(${args.map((arg) => inputValue(arg, 0, includeDescription)).join(", ")})`;
  }
  return `${description(value.description, depth, includeDescription)}${"  ".repeat(depth)}${value.name}${printedArgs}: ${value.type}${directives(value.astNode?.directives, value.deprecationReason)}\n`;
}

function enumValue(value: GraphQLEnumValue, depth: number, includeDescription: boolean): string {
  return `${description(value.description, depth, includeDescription)}${"  ".repeat(depth)}${value.name}${directives(value.astNode?.directives, value.deprecationReason)}\n`;
}

function definition(value: GraphQLNamedType, elementDescription: string | null, includeDescription: boolean): string {
  const prefix = isScalarType(value) ? "scalar" : isObjectType(value) ? "type" : isInterfaceType(value) ? "interface" : isUnionType(value) ? "union" : isEnumType(value) ? "enum" : "input";
  let rendered = `${description(elementDescription, 0, includeDescription)}${prefix} ${value.name}`;
  if (isObjectType(value) || isInterfaceType(value)) {
    const interfaces = value.getInterfaces();
    if (interfaces.length) rendered += ` implements ${interfaces.map((entry) => entry.name).join(" & ")}`;
  }
  const fallback = isScalarType(value) && value.specifiedByURL ? ` @specifiedBy(url: ${quoted(value.specifiedByURL)})` : isInputObjectType(value) && value.isOneOf ? " @oneOf" : "";
  rendered += directives(value.astNode?.directives, undefined, fallback);
  if (isUnionType(value)) {
    const members = value.getTypes();
    if (members.length) rendered += ` = ${members.map((member) => member.name).join(" | ")}`;
  }
  let members = "";
  if (isObjectType(value) || isInterfaceType(value)) {
    members = Object.values(value.getFields()).filter((member) => !member.name.startsWith("__")).map((member) => field(member, 1, includeDescription)).join("");
  } else if (isInputObjectType(value)) {
    members = Object.values(value.getFields()).filter((member) => !member.name.startsWith("__")).map((member) => inputValue(member, 1, includeDescription)).join("");
  } else if (isEnumType(value)) {
    members = value.getValues().map((member) => enumValue(member, 1, includeDescription)).join("");
  }
  return members ? `${rendered} {\n${members}}` : rendered;
}

/** Render one declaration on demand, without storing SDL in the graph. */
export function renderElement(element: ExplorerElement, includeDescription = false): string {
  switch (element.kind) {
    case "TYPE": return definition(element.value as GraphQLNamedType, element.description, includeDescription);
    case "FIELD": return field(element.value as GraphQLField<unknown, unknown>, 0, includeDescription).slice(0, -1);
    case "INPUT_VALUE": return inputValue(element.value as InputValue, 0, includeDescription);
    case "ENUM_VALUE": return enumValue(element.value as GraphQLEnumValue, 0, includeDescription).slice(0, -1);
  }
}
