package com.example;

// Audit ends in "it". It is production code, not an integration test, and
// witness must never hand it to a test runner.
public class Audit {
    public String record(String event) {
        return event;
    }
}
