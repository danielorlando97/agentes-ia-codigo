// Chatbot: conversacion turn-by-turn con memoria de sesion. Sin tools, sin loop autonomo.

// Cómo ejecutar: make ts SCRIPT=typescript/01-que-es-un-agente/chatbot.ts
// Qué esperar: prompt interactivo, escribe mensajes, responde el modelo. 'salir' para terminar.

import Anthropic from "@anthropic-ai/sdk";
import * as readline from "readline";

const MODEL = process.env["MODEL"] ?? "claude-sonnet-4-6";
const SYSTEM = "Eres un asistente util. Responde de forma concisa.";

const client = new Anthropic();
const session: Anthropic.MessageParam[] = [];
const rl = readline.createInterface({ input: process.stdin, output: process.stdout });

function ask(prompt: string): Promise<string> {
  return new Promise((resolve) => rl.question(prompt, resolve));
}

async function chat() {
  console.log("Chatbot iniciado. Escribe 'salir' para terminar.");
  while (true) {
    const msg = await ask("> ");
    if (msg.trim().toLowerCase() === "salir") break;
    session.push({ role: "user", content: msg });
    const response = await client.messages.create({
      model: MODEL,
      max_tokens: 1024,
      system: SYSTEM,
      messages: session,
    });
    const text = response.content
      .filter((b): b is Anthropic.TextBlock => b.type === "text")
      .map((b) => b.text)
      .join("");
    session.push({ role: "assistant", content: text });
    console.log(text);
  }
  rl.close();
}

chat();
