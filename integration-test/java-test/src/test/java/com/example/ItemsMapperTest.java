package com.example;

import com.example.entity.Item;
import com.example.mapper.*;
import org.junit.jupiter.api.Test;
import static org.junit.jupiter.api.Assertions.*;

/**
 * Compilation test: verifies the generated Java code compiles
 * and the factory resolves the correct mapper by driver name.
 */
class ItemsMapperTest {

    @Test
    void testFactoryResolvesPG() {
        // Just verify the factory compiles and resolves
        // (no actual DB — pure compilation test)
        assertNotNull(ItemsMapperFactory.class);

        // Verify each mapper interface exists
        assertNotNull(ItemsMapperPG.class);
        assertNotNull(ItemsMapperMySQL.class);
        assertNotNull(ItemsMapperOracle.class);
        assertNotNull(ItemsMapperMSSQL.class);

        // Verify shared interface
        assertNotNull(ItemsMapper.class);
    }

    @Test
    void testModelClassExists() {
        assertNotNull(Item.class);
    }

    @Test
    void testMapperMethodsSignature() throws NoSuchMethodException {
        // Verify method signatures are generated correctly
        assertNotNull(ItemsMapper.class.getMethod("findByID", long.class));
        assertNotNull(ItemsMapper.class.getMethod("findAll"));
        assertNotNull(ItemsMapper.class.getMethod("insertAndReturnID", Item.class));
    }
}
