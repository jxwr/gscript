// state_machine.gs - State machine implementation in GScript
// Demonstrates: state machine, transitions with guards, entry/exit actions,
//               traffic light demo, order processing demo

print("=== State Machine ===")
print()

// -------------------------------------------------------
// 1. State Machine Implementation
// -------------------------------------------------------

func newStateMachine(config) {
    sm := {}
    currentState := config.initial
    states := config.states
    onTransition := config.onTransition

    // Get current state
    sm.getState = func() {
        return currentState
    }

    // Check if a transition is possible
    sm.can = func(event) {
        stateConfig := states[currentState]
        if stateConfig == nil { return false }
        transitions := stateConfig.transitions
        if transitions == nil { return false }
        t := transitions[event]
        if t == nil { return false }

        // Check guard if present
        if t.guard != nil {
            return t.guard()
        }
        return true
    }

    // Send an event to the state machine
    sm.send = func(event, ...) {
        args := {...}
        stateConfig := states[currentState]
        if stateConfig == nil {
            return false, "unknown state: " .. currentState
        }

        transitions := stateConfig.transitions
        if transitions == nil {
            return false, "no transitions from state: " .. currentState
        }

        t := transitions[event]
        if t == nil {
            return false, "no transition for event '" .. event .. "' in state '" .. currentState .. "'"
        }

        // Check guard
        if t.guard != nil && !t.guard() {
            return false, "guard rejected transition"
        }

        fromState := currentState
        toState := t.target

        // Execute exit action of current state
        if stateConfig.onExit != nil {
            stateConfig.onExit(fromState, event)
        }

        // Execute transition action
        if t.action != nil {
            t.action(fromState, toState, unpack(args))
        }

        // Change state
        currentState = toState

        // Execute entry action of new state
        newStateConfig := states[toState]
        if newStateConfig != nil && newStateConfig.onEntry != nil {
            newStateConfig.onEntry(toState, event)
        }

        // Global transition callback
        if onTransition != nil {
            onTransition(fromState, toState, event)
        }

        return true, nil
    }

    // Execute entry action for initial state
    initialConfig := states[currentState]
    if initialConfig != nil && initialConfig.onEntry != nil {
        initialConfig.onEntry(currentState, "init")
    }

    return sm
}

// -------------------------------------------------------
// 2. Demo: Traffic Light State Machine
// -------------------------------------------------------
print("--- Traffic Light State Machine ---")

trafficLight := newStateMachine({
    initial: "red",
    onTransition: func(from, to, event) {
        print(string.format("    [%s] --%s--> [%s]", from, event, to))
    },
    states: {
        red: {
            onEntry: func(state, event) {
                print("    >> RED: Stop! All traffic must stop.")
            },
            onExit: func(state, event) {
                print("    << Leaving RED")
            },
            transitions: {
                timer: {target: "green"}
            }
        },
        green: {
            onEntry: func(state, event) {
                print("    >> GREEN: Go! Traffic may proceed.")
            },
            onExit: func(state, event) {
                print("    << Leaving GREEN")
            },
            transitions: {
                timer: {target: "yellow"}
            }
        },
        yellow: {
            onEntry: func(state, event) {
                print("    >> YELLOW: Caution! Prepare to stop.")
            },
            onExit: func(state, event) {
                print("    << Leaving YELLOW")
            },
            transitions: {
                timer: {target: "red"}
            }
        }
    }
})

print("  Initial state:", trafficLight.getState())
print()

// Cycle through the traffic light
events := {"timer", "timer", "timer", "timer", "timer", "timer"}
for i := 1; i <= #events; i++ {
    print(string.format("  --- Sending '%s' event ---", events[i]))
    trafficLight.send(events[i])
    print()
}

// -------------------------------------------------------
// 3. Demo: Order Processing State Machine
// -------------------------------------------------------
print("--- Order Processing State Machine ---")

// Simulated order context
orderData := {
    id: "ORD-001",
    items: {"Widget", "Gadget"},
    total: 59.99,
    paid: false,
    shipped: false
}

orderLog := {}

func logOrder(msg) {
    table.insert(orderLog, msg)
    print("    " .. msg)
}

orderSM := newStateMachine({
    initial: "created",
    onTransition: func(from, to, event) {
        logOrder(string.format("Order %s: [%s] --%s--> [%s]", orderData.id, from, event, to))
    },
    states: {
        created: {
            onEntry: func(state, event) {
                logOrder("Order created with " .. #orderData.items .. " items, total: $" .. tostring(orderData.total))
            },
            transitions: {
                pay: {
                    target: "paid",
                    action: func(from, to) {
                        orderData.paid = true
                        logOrder("Payment received!")
                    }
                },
                cancel: {
                    target: "cancelled",
                    action: func(from, to) {
                        logOrder("Order cancelled by customer")
                    }
                }
            }
        },
        paid: {
            onEntry: func(state, event) {
                logOrder("Payment confirmed. Ready for processing.")
            },
            transitions: {
                process: {
                    target: "processing",
                    guard: func() {
                        return orderData.paid
                    },
                    action: func(from, to) {
                        logOrder("Processing order...")
                    }
                },
                refund: {
                    target: "refunded",
                    action: func(from, to) {
                        orderData.paid = false
                        logOrder("Refund issued")
                    }
                }
            }
        },
        processing: {
            onEntry: func(state, event) {
                logOrder("Order is being prepared for shipment")
            },
            transitions: {
                ship: {
                    target: "shipped",
                    action: func(from, to) {
                        orderData.shipped = true
                        logOrder("Order shipped! Tracking: GS-12345")
                    }
                },
                fail: {
                    target: "failed",
                    action: func(from, to) {
                        logOrder("Processing failed!")
                    }
                }
            }
        },
        shipped: {
            onEntry: func(state, event) {
                logOrder("Order is on its way!")
            },
            transitions: {
                deliver: {
                    target: "delivered",
                    action: func(from, to) {
                        logOrder("Order delivered successfully!")
                    }
                },
                returnItem: {
                    target: "returned",
                    action: func(from, to) {
                        logOrder("Return initiated")
                    }
                }
            }
        },
        delivered: {
            onEntry: func(state, event) {
                logOrder("Order complete! Thank you for your purchase.")
            },
            transitions: {
                returnItem: {
                    target: "returned",
                    action: func(from, to) {
                        logOrder("Return initiated after delivery")
                    }
                }
            }
        },
        cancelled: {
            onEntry: func(state, event) {
                logOrder("Order has been cancelled.")
            },
            transitions: {}
        },
        refunded: {
            onEntry: func(state, event) {
                logOrder("Refund processed.")
            },
            transitions: {}
        },
        failed: {
            onEntry: func(state, event) {
                logOrder("Order processing failed. Please contact support.")
            },
            transitions: {
                retry: {
                    target: "processing",
                    action: func(from, to) {
                        logOrder("Retrying order processing...")
                    }
                }
            }
        },
        returned: {
            onEntry: func(state, event) {
                logOrder("Return received. Refund in progress.")
            },
            transitions: {}
        }
    }
})

print()

// Happy path: create -> pay -> process -> ship -> deliver
print("  === Happy Path ===")
print()
orderSM.send("pay")
print()
orderSM.send("process")
print()
orderSM.send("ship")
print()
orderSM.send("deliver")
print()
print("  Final state:", orderSM.getState())
print()

// Try invalid transition
print("  === Invalid Transition ===")
ok, err := orderSM.send("pay")
print("  Send 'pay' in 'delivered' state - ok:", ok, "err:", err)
print()

// -------------------------------------------------------
// 4. State machine with context: Elevator
// -------------------------------------------------------
print("--- Elevator State Machine ---")

elevator := {floor: 1, doorsOpen: false}

elevatorSM := newStateMachine({
    initial: "idle",
    onTransition: func(from, to, event) {
        print(string.format("    Elevator: [%s] --%s--> [%s] (floor %d)",
            from, event, to, elevator.floor))
    },
    states: {
        idle: {
            onEntry: func(state, event) {
                print(string.format("    Elevator idle at floor %d", elevator.floor))
            },
            transitions: {
                call: {
                    target: "moving",
                    action: func(from, to, targetFloor) {
                        print(string.format("    Called to floor %d", targetFloor))
                    }
                },
                openDoors: {
                    target: "doorsOpen",
                    action: func(from, to) {
                        elevator.doorsOpen = true
                    }
                }
            }
        },
        moving: {
            onEntry: func(state, event) {
                print("    Elevator is moving...")
            },
            transitions: {
                arrive: {
                    target: "doorsOpen",
                    action: func(from, to, floor) {
                        elevator.floor = floor
                        elevator.doorsOpen = true
                        print(string.format("    Arrived at floor %d, opening doors", floor))
                    }
                },
                emergency: {
                    target: "emergency",
                    action: func(from, to) {
                        print("    EMERGENCY STOP!")
                    }
                }
            }
        },
        doorsOpen: {
            onEntry: func(state, event) {
                print("    Doors are open")
            },
            transitions: {
                closeDoors: {
                    target: "idle",
                    action: func(from, to) {
                        elevator.doorsOpen = false
                        print("    Doors closed")
                    }
                }
            }
        },
        emergency: {
            onEntry: func(state, event) {
                print("    EMERGENCY MODE - elevator stopped")
            },
            transitions: {
                reset: {
                    target: "idle",
                    action: func(from, to) {
                        print("    Emergency reset - returning to normal operation")
                    }
                }
            }
        }
    }
})

print()
elevatorSM.send("call", 5)
print()
elevatorSM.send("arrive", 5)
print()
elevatorSM.send("closeDoors")
print()
elevatorSM.send("call", 1)
print()
elevatorSM.send("emergency")
print()
elevatorSM.send("reset")
print()

// -------------------------------------------------------
// 5. Guard conditions demo
// -------------------------------------------------------
print("--- Guard Conditions ---")

doorLocked := true

doorSM := newStateMachine({
    initial: "closed",
    onTransition: func(from, to, event) {
        print(string.format("    Door: [%s] --%s--> [%s]", from, event, to))
    },
    states: {
        closed: {
            transitions: {
                open: {
                    target: "open",
                    guard: func() {
                        if doorLocked {
                            print("    Guard: Door is locked! Cannot open.")
                            return false
                        }
                        return true
                    }
                },
                unlock: {
                    target: "closed",
                    action: func(from, to) {
                        doorLocked = false
                        print("    Door unlocked")
                    }
                }
            }
        },
        open: {
            transitions: {
                close: {
                    target: "closed",
                    action: func(from, to) {
                        print("    Door closed")
                    }
                }
            }
        }
    }
})

print()
print("  Try to open locked door:")
ok, err = doorSM.send("open")
print("  Result - ok:", ok, "err:", err)
print()

print("  Unlock the door:")
doorSM.send("unlock")
print()

print("  Try to open again:")
ok, err = doorSM.send("open")
print("  Result - ok:", ok)
print()

print("  Close the door:")
doorSM.send("close")
print()

print("=== Done ===")
