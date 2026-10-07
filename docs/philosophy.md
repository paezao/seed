# Philosophy

Most AI software development has the same shape: a coding agent outside the software reads a
repository, reconstructs what it is for, changes it, and leaves. The software never knows what
happened to it. Every session starts from archaeology.

Seed inverts this. **The builder is part of the software.** A Seed is the smallest
application that knows how to become a larger application:

- It begins nearly empty. It does not pretend to be anything, and it says so: *I don't have a purpose yet.*
- Its owner talks to it, and it changes **itself**. Its code, its schema, its UI, its name and its
  logo all change. What lives at `/` is the Seed in its current form, not an app it generated.
- The agent that made the change stays inside. It remembers the intent, the plan, the
  decisions and the trade-offs, because it was there.

## Design rules

1. **Capability to create, not the capability itself.** For anything we were tempted to put in
   the initial Seed, we asked: does the Seed need this, or only the ability to create it? The
   kernel holds only what it takes to grow safely: an agent loop, primitive tools, a sandbox,
   git, memory, skills and a control plane. Everything else is grown.
2. **Continuity of identity.** After ten evolutions, a Seed should know what it is, why it
   exists, what it can do, which decisions shaped it, which skills it has learned, and how it
   reached its current generation. That knowledge lives in readable files committed with each
   generation, not in a vector store.
3. **No fake intelligence.** There are no templates per application type, and no `if prompt
   contains "todo"`. A todo app is produced by the same general mechanism as anything else.
4. **Ordinary software.** The organism is plain Go, React and PostgreSQL that a human can read
   and take over. Seed is an evolutionary runtime around software, not a new language.
5. **Boring infrastructure.** Go, PostgreSQL, Git and Docker, with no agent framework, queue or
   vector DB. The Seed must be able to understand its own implementation.
6. **Safety by construction.** Self-modifying software needs strong trust boundaries. Every
   change happens in isolation, is independently verified, is a git commit, and can be rolled
   back. The kernel is protected from the organism it grows.

## What it is not

- Not an IDE with an assistant. There is no editor, and the conversation is the interface.
- Not a wrapper around an external coding agent. The agent harness is about 300 lines in
  `kernel/agent`.
- Not "generate me an app". Generation is the first step, and evolution is the product.
