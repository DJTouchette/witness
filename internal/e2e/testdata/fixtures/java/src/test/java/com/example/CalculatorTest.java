package com.example;

import static org.junit.jupiter.api.Assertions.assertEquals;

import org.junit.jupiter.api.Test;

class CalculatorTest {
    @Test
    void addsNumbers() {
        assertEquals(3, new Calculator().add(1, 2));
    }
}
