// Cómo ejecutar: make ts SCRIPT=typescript/10-decisiones/routing.ts
/**
 * Router en cascada: keyword → Jaccard → LLM.
 *
 * Tres mecanismos ordenados por costo creciente. Solo sube a la siguiente
 * capa si la actual no produce match.
 *
 * Requiere: npm install @anthropic-ai/sdk
 */
import Anthropic from "@anthropic-ai/sdk";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";
const JACCARD_THRESHOLD = 0.15;

interface Route {
  name: string;
  description: string;
  keywords: string[];
  examples: string[];
}

const DEFAULT_ROUTE: Route = {
  name: "DEFAULT",
  description: "Ruta de fallback para inputs que no encajan en ninguna especialización",
  keywords: [],
  examples: [],
};

function jaccard(textA: string, textB: string): number {
  const wordsA = new Set(textA.toLowerCase().split(/\s+/));
  const wordsB = new Set(textB.toLowerCase().split(/\s+/));
  const union = new Set([...wordsA, ...wordsB]);
  if (union.size === 0) return 0;
  const inter = [...wordsA].filter((w) => wordsB.has(w));
  return inter.length / union.size;
}

function routerKeyword(userInput: string, routes: Route[]): Route | null {
  const lower = userInput.toLowerCase();
  for (const route of routes) {
    if (route.keywords.some((kw) => lower.includes(kw.toLowerCase()))) {
      return route;
    }
  }
  return null;
}

function routerJaccard(userInput: string, routes: Route[]): Route | null {
  let bestRoute: Route | null = null;
  let bestScore = 0;

  for (const route of routes) {
    const score =
      route.examples.length > 0
        ? Math.max(...route.examples.map((ex) => jaccard(userInput, ex)))
        : 0;
    if (score > bestScore) {
      bestScore = score;
      bestRoute = route;
    }
  }

  return bestScore >= JACCARD_THRESHOLD ? bestRoute : null;
}

async function routerLLM(
  userInput: string,
  routes: Route[],
  client: Anthropic
): Promise<Route> {
  const routeList = routes
    .map((r) => `- ${r.name}: ${r.description}`)
    .join("\n");

  const prompt = `Clasifica el siguiente input en una de estas rutas:
${routeList}
- DEFAULT: ninguna de las anteriores

Input: ${userInput}

Responde ÚNICAMENTE con JSON válido:
{"destination": "<nombre_ruta>", "next_inputs": "<input reformulado si aplica, sino igual al original>"}`;

  const response = await client.messages.create({
    model: MODEL,
    max_tokens: 256,
    messages: [{ role: "user", content: prompt }],
  });

  const text =
    response.content[0].type === "text" ? response.content[0].text : "";

  // El LLM puede rodear el JSON con markdown — extraemos solo el objeto
  const match = text.match(/\{[\s\S]*\}/);
  if (!match) return DEFAULT_ROUTE;

  const parsed = JSON.parse(match[0]) as { destination?: string };
  const destination = parsed.destination ?? "DEFAULT";

  const routeMap = new Map(routes.map((r) => [r.name, r]));
  return routeMap.get(destination) ?? DEFAULT_ROUTE;
}

async function cascadeRouter(
  userInput: string,
  routes: Route[],
  client: Anthropic
): Promise<[Route, string]> {
  const kw = routerKeyword(userInput, routes);
  if (kw) return [kw, "keyword"];

  const jac = routerJaccard(userInput, routes);
  if (jac) return [jac, "jaccard"];

  const llm = await routerLLM(userInput, routes, client);
  return [llm, "llm"];
}

// --- Demo ---

const ROUTES: Route[] = [
  {
    name: "soporte_tecnico",
    description: "Problemas técnicos con el producto, errores, bugs, configuración",
    keywords: ["error", "falla", "bug", "no funciona", "excepción", "crash"],
    examples: [
      "el endpoint de autenticación devuelve 500",
      "no puedo conectarme a la API",
      "el SDK lanza una excepción al inicializar",
      "la integración de webhook falla con timeout",
    ],
  },
  {
    name: "facturacion",
    description: "Preguntas sobre pagos, facturas, planes, precios, suscripciones",
    keywords: ["factura", "pago", "precio", "suscripción", "plan", "cobro"],
    examples: [
      "quiero cambiar mi plan de facturación mensual a anual",
      "no me llegó la factura de este mes",
      "cómo cancelo mi suscripción",
      "cuánto cuesta el plan enterprise",
    ],
  },
  {
    name: "general",
    description: "Preguntas generales sobre la empresa, el producto, horarios, contacto",
    keywords: ["horario", "contacto", "email", "teléfono", "dirección"],
    examples: [
      "cuál es el horario de atención al cliente",
      "cómo puedo contactar con soporte por correo",
      "dónde están ubicadas las oficinas",
    ],
  },
];

async function main(): Promise<void> {
  const client = new Anthropic();

  const queries = [
    "Tengo un bug en el SDK que hace crash la app al iniciar",
    "necesito cambiar cómo me cobran cada mes al plan anual",
    "¿cuánto tiempo lleva aproximadamente resolver una disputa de cargo?",
  ];

  console.log("=== Router en cascada ===\n");
  for (const query of queries) {
    const [route, mechanism] = await cascadeRouter(query, ROUTES, client);
    console.log(`Input    : ${query}`);
    console.log(`Ruta     : ${route.name}`);
    console.log(`Mecanismo: ${mechanism}`);
    console.log();
  }
}

main().catch(console.error);
