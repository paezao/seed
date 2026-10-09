# Spending

A Seed thinks with a language model, and that costs money. Every model call
is written down: what it was for, how many tokens it used, and what the
provider charged. OpenRouter reports the cost of each call; calls with no
reported price are counted as $0 and flagged on the page.

## Where it goes

The **Spending** page in the control plane shows this month (UTC):

- the total, against the budget if there is one;
- a bar per day;
- what it went on: **Chat**, **Evolutions**, **Routines**, **Health** (my
  doctor investigating problems) and **Other**;
- the most expensive evolutions, routines and incidents, linked.

Each evolution's technical details also show what it cost.

## A monthly budget

Set a budget on the Spending page. Once this month's spending reaches it:

- scheduled **agent routines** don't run (the run is recorded as *paused
  (budget)*); jobs, which don't use the model, still run;
- the doctor doesn't **investigate** problems or **fix them on its own**
  (incidents are still recorded, and say why they're waiting);
- the Seed says so once in chat, and the Spending link in the sidebar gets a
  dot.

What the owner asks for still happens: chat, evolutions, running a routine by
hand, asking the doctor to investigate again or to fix something. The owner
sees the spending, and it's their call.

The pause lifts at the start of the next month, or as soon as the budget is
raised or removed.

## API

- `GET /_seed/api/spending`: this month's report, the budget and whether
  spending on my own is paused.
- `POST /_seed/api/spending/budget` `{"budget_usd": 25}`: set the budget
  (`0` removes it).
